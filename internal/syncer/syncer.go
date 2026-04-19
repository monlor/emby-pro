package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
	"github.com/monlor/emby-pro/internal/redirect"
)

const minRunSleep = time.Second

type Syncer struct {
	cfg    config.Config
	store  *index.Store
	client *openlist.Client
	logger *log.Logger
	rules  map[string]config.Rule
	redir  *redirect.Builder
	rng    *rand.Rand
}

type fileWrite struct {
	sourcePath  string
	parentPath  string
	targetPath  string
	content     string
	contentHash string
	size        int64
	name        string
}

func New(cfg config.Config, store *index.Store, client *openlist.Client) *Syncer {
	rules := make(map[string]config.Rule, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		rules[rule.Name] = rule
	}
	return &Syncer{
		cfg:    cfg,
		store:  store,
		client: client,
		logger: log.New(os.Stdout, "[emby-pro] ", log.LstdFlags),
		rules:  rules,
		redir:  redirect.NewBuilder(cfg.Redirect),
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *Syncer) Run(ctx context.Context) error {
	if err := s.bootstrap(); err != nil {
		return err
	}

	for {
		if err := s.runCycle(ctx); err != nil {
			s.warnf("sync cycle failed: %v", err)
		}

		sleepFor, err := s.nextRunSleep(time.Now())
		if err != nil {
			return err
		}

		timer := time.NewTimer(sleepFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (s *Syncer) RunOnce(ctx context.Context) error {
	if err := s.bootstrap(); err != nil {
		return err
	}
	return s.runCycle(ctx)
}

func (s *Syncer) nextRunSleep(now time.Time) (time.Duration, error) {
	nextWakeAt, err := s.nextWakeAt(now)
	if err != nil {
		return 0, err
	}
	if nextWakeAt.IsZero() || !nextWakeAt.After(now) {
		return minRunSleep, nil
	}
	sleepFor := nextWakeAt.Sub(now)
	if sleepFor < minRunSleep {
		return minRunSleep, nil
	}
	return sleepFor, nil
}

func (s *Syncer) nextWakeAt(now time.Time) (time.Time, error) {
	nextAt := time.Time{}

	nextEligibleScanAt, err := s.store.NextEligibleScanAt()
	if err != nil {
		return time.Time{}, fmt.Errorf("load next eligible scan: %w", err)
	}
	if !nextEligibleScanAt.IsZero() {
		nextAt = nextEligibleScanAt
	}

	states, err := s.store.ListRuleStates()
	if err != nil {
		return time.Time{}, fmt.Errorf("load rule states: %w", err)
	}
	for _, state := range states {
		rescanAt := state.LastFullRescanAt.Add(s.cfg.Sync.FullRescanInterval)
		if state.LastFullRescanAt.IsZero() {
			rescanAt = now
		}
		if nextAt.IsZero() || rescanAt.Before(nextAt) {
			nextAt = rescanAt
		}
	}

	return nextAt, nil
}

func (s *Syncer) bootstrap() error {
	now := time.Now()
	if err := os.MkdirAll(s.cfg.Sync.BaseDir, 0o755); err != nil {
		return fmt.Errorf("create base dir: %w", err)
	}
	if err := s.cleanupStaleRules(); err != nil {
		return err
	}
	for _, rule := range s.cfg.Rules {
		if err := os.MkdirAll(rule.TargetPath, 0o755); err != nil {
			return fmt.Errorf("create rule target dir %s: %w", rule.TargetPath, err)
		}
		if err := s.store.EnsureRule(rule, now); err != nil {
			return fmt.Errorf("ensure rule %s: %w", rule.Name, err)
		}
	}
	return nil
}

func (s *Syncer) runCycle(ctx context.Context) error {
	now := time.Now()
	for _, rule := range s.cfg.Rules {
		shouldRescan, err := s.store.ShouldScheduleFullRescan(rule.Name, s.cfg.Sync.FullRescanInterval, now)
		if err != nil {
			return fmt.Errorf("check full rescan for %s: %w", rule.Name, err)
		}
		if shouldRescan {
			if err := s.store.ScheduleFullRescan(rule.Name, now); err != nil {
				return fmt.Errorf("schedule full rescan for %s: %w", rule.Name, err)
			}
			s.debugf("scheduled full rescan for rule=%s", rule.Name)
		}
	}

	requestBudget := s.cfg.Sync.MaxRequestsPerCycle
	processed := 0
	processedInCycle := make(map[string]struct{})

	for processed < s.cfg.Sync.MaxDirsPerCycle {
		if requestBudget <= 0 {
			s.debugf("request budget exhausted after %d directories", processed)
			break
		}

		remaining := s.cfg.Sync.MaxDirsPerCycle - processed
		dirs, err := s.store.DueDirs(remaining, time.Now())
		if err != nil {
			return fmt.Errorf("load due dirs: %w", err)
		}
		if len(dirs) == 0 {
			if processed == 0 {
				s.debugf("no due directories")
			}
			break
		}

		madeProgress := false
		for _, dir := range dirs {
			if requestBudget <= 0 || processed >= s.cfg.Sync.MaxDirsPerCycle {
				break
			}

			dirKey := dir.RuleName + "\x00" + dir.SourcePath
			if _, ok := processedInCycle[dirKey]; ok {
				continue
			}
			processedInCycle[dirKey] = struct{}{}
			madeProgress = true

			rule, ok := s.rules[dir.RuleName]
			if !ok {
				continue
			}
			if err := s.scanDir(ctx, rule, dir, &requestBudget); err != nil {
				s.warnf("scan failed rule=%s dir=%s err=%v", rule.Name, dir.SourcePath, err)
			}
			processed++
		}

		if !madeProgress {
			break
		}
	}

	s.infof("cycle complete processed_dirs=%d remaining_request_budget=%d", processed, requestBudget)
	return nil
}

func (s *Syncer) scanDir(ctx context.Context, rule config.Rule, dir index.DirRecord, requestBudget *int) error {
	now := time.Now()
	entries, err := s.fetchEntries(ctx, dir.SourcePath, requestBudget)
	if err != nil {
		nextRetry, cooldownUntil, result := s.nextFailureSchedule(dir, now, err)
		if isWindControlError(err) {
			if cooldownErr := s.store.SetRuleCooldown(rule.Name, cooldownUntil); cooldownErr != nil {
				s.warnf("set rule cooldown failed rule=%s err=%v", rule.Name, cooldownErr)
			} else {
				s.warnf("rule cooldown activated rule=%s until=%s reason=%v", rule.Name, cooldownUntil.Format(time.RFC3339), err)
			}
		}
		if markErr := s.store.MarkDirScannedFailed(rule.Name, dir.SourcePath, err.Error(), nextRetry, cooldownUntil, now, result); markErr != nil {
			s.warnf("mark dir failed rule=%s dir=%s err=%v", rule.Name, dir.SourcePath, markErr)
		}
		return err
	}

	existingFiles, err := s.store.ListFilesByParent(rule.Name, dir.SourcePath)
	if err != nil {
		return fmt.Errorf("list existing files: %w", err)
	}
	existingDirs, err := s.store.ListChildDirs(rule.Name, dir.SourcePath)
	if err != nil {
		return fmt.Errorf("list child dirs: %w", err)
	}

	writes := make([]fileWrite, 0, len(entries))
	seenFiles := make(map[string]struct{})
	seenDirs := make(map[string]struct{})

	for _, entry := range entries {
		sourcePath := joinSourcePath(dir.SourcePath, entry.Name)
		if entry.IsDir {
			seenDirs[sourcePath] = struct{}{}
			if err := s.store.UpsertDir(rule.Name, sourcePath, dir.SourcePath, dir.Depth+1, now); err != nil {
				return fmt.Errorf("upsert dir %s: %w", sourcePath, err)
			}
			continue
		}

		if !s.isVideo(entry.Name) {
			continue
		}
		if entry.Size < s.cfg.Sync.MinFileSize {
			continue
		}

		relPath := relativeSourcePath(rule.SourcePath, sourcePath)
		if !rule.ShouldKeep(relPath) {
			continue
		}

		content, err := s.buildSTRMContent(sourcePath)
		if err != nil {
			return fmt.Errorf("build strm content for %s: %w", sourcePath, err)
		}

		targetPath := s.targetPathFor(rule, sourcePath)
		hash := sha256.Sum256([]byte(content))
		writes = append(writes, fileWrite{
			sourcePath:  sourcePath,
			parentPath:  dir.SourcePath,
			targetPath:  targetPath,
			content:     content,
			contentHash: hex.EncodeToString(hash[:]),
			size:        entry.Size,
			name:        entry.Name,
		})
	}

	writes, activeTargets := s.resolveWriteConflicts(writes)
	for _, write := range writes {
		seenFiles[write.sourcePath] = struct{}{}
	}
	scheduleChanged := hasScheduleChange(existingFiles, existingDirs, writes, seenDirs)

	existingFileMap := make(map[string]index.FileRecord, len(existingFiles))
	for _, file := range existingFiles {
		existingFileMap[file.SourcePath] = file
	}

	actualWrites := 0
	removedFiles := 0
	removedDirs := 0

	for _, write := range writes {
		existing, exists := existingFileMap[write.sourcePath]
		shouldTrack, wrote, err := s.writeOne(rule, write, existing, exists)
		if err != nil {
			return err
		}
		if wrote {
			actualWrites++
		}
		if !shouldTrack {
			continue
		}
		if err := s.store.UpsertFile(index.FileRecord{
			RuleName:    rule.Name,
			SourcePath:  write.sourcePath,
			ParentPath:  write.parentPath,
			TargetPath:  write.targetPath,
			ContentHash: write.contentHash,
			LastSeenAt:  now,
		}); err != nil {
			return fmt.Errorf("upsert file %s: %w", write.sourcePath, err)
		}
	}

	if rule.CleanRemovedValue(s.cfg.Sync.CleanRemoved) {
		for _, file := range existingFiles {
			if _, ok := seenFiles[file.SourcePath]; ok {
				continue
			}
			if _, ok := activeTargets[file.TargetPath]; !ok {
				if err := os.Remove(file.TargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					s.warnf("remove file %s: %v", file.TargetPath, err)
				} else {
					removedFiles++
				}
			}
			if err := s.store.DeleteFile(rule.Name, file.SourcePath); err != nil {
				return fmt.Errorf("delete file %s: %w", file.SourcePath, err)
			}
			if !rule.FlattenValue() {
				if _, ok := activeTargets[file.TargetPath]; ok {
					continue
				}
				s.removeEmptyParents(filepath.Dir(file.TargetPath), rule.TargetPath)
			}
		}

		for _, childDir := range existingDirs {
			if _, ok := seenDirs[childDir]; ok {
				continue
			}
			if !rule.FlattenValue() {
				localDir := s.targetDirFor(rule, childDir)
				if localDir != rule.TargetPath {
					if err := os.RemoveAll(localDir); err != nil {
						s.warnf("remove dir %s: %v", localDir, err)
					} else {
						removedDirs++
					}
					s.removeEmptyParents(filepath.Dir(localDir), rule.TargetPath)
				}
			}
			if err := s.store.DeleteDirSubtree(rule.Name, childDir); err != nil {
				return fmt.Errorf("delete subtree %s: %w", childDir, err)
			}
		}
	}

	listingState := summarizeEntries(entries)
	nextScanAt, unchangedStreak, result := s.nextSuccessSchedule(dir, scheduleChanged, now)
	if err := s.store.MarkDirScannedSuccess(rule.Name, dir.SourcePath, nextScanAt, listingState.latestModified, listingState.entryCount, result, unchangedStreak, now); err != nil {
		return fmt.Errorf("mark dir success: %w", err)
	}

	if actualWrites > 0 || removedFiles > 0 || removedDirs > 0 {
		s.infof(
			"scanned rule=%s dir=%s entries=%d candidates=%d wrote=%d removed_files=%d removed_dirs=%d",
			rule.Name,
			dir.SourcePath,
			len(entries),
			len(writes),
			actualWrites,
			removedFiles,
			removedDirs,
		)
	}
	return nil
}

func (s *Syncer) fetchEntries(ctx context.Context, sourcePath string, requestBudget *int) ([]openlist.Entry, error) {
	pageNum := 1
	entries := make([]openlist.Entry, 0)

	for {
		if *requestBudget <= 0 {
			return nil, fmt.Errorf("request budget exhausted while scanning %s", sourcePath)
		}
		*requestBudget--

		page, err := s.client.ListPage(ctx, sourcePath, pageNum, s.cfg.OpenList.ListPerPage)
		if err != nil {
			return nil, err
		}
		if len(page.Content) == 0 {
			break
		}

		entries = append(entries, page.Content...)
		if len(entries) >= page.Total || len(page.Content) < s.cfg.OpenList.ListPerPage {
			break
		}
		pageNum++
	}

	return entries, nil
}

func (s *Syncer) buildSTRMContent(sourcePath string) (string, error) {
	publicPath := config.MapSourceToPublicPath(s.cfg.Redirect.PathMappings, sourcePath)
	return s.redir.Build(publicPath)
}

func (s *Syncer) writeOne(rule config.Rule, write fileWrite, existing index.FileRecord, exists bool) (shouldTrack bool, wrote bool, err error) {
	if err := os.MkdirAll(filepath.Dir(write.targetPath), 0o755); err != nil {
		return false, false, fmt.Errorf("create target dir for %s: %w", write.targetPath, err)
	}

	if !exists {
		if _, err := os.Stat(write.targetPath); err == nil {
			inUse, useErr := s.store.TargetInUseByAnotherSource(rule.Name, write.sourcePath, write.targetPath)
			if useErr != nil {
				return false, false, fmt.Errorf("check tracked target %s: %w", write.targetPath, useErr)
			}
			if inUse {
				s.warnf("skip conflicting tracked file target=%s source=%s", write.targetPath, write.sourcePath)
				return false, false, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, false, fmt.Errorf("stat target %s: %w", write.targetPath, err)
		}
	}

	if exists && !rule.OverwriteValue(s.cfg.Sync.Overwrite) &&
		existing.ContentHash == write.contentHash && existing.TargetPath == write.targetPath {
		return true, false, nil
	}
	if exists && existing.TargetPath != write.targetPath {
		if err := os.Remove(existing.TargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, false, fmt.Errorf("remove old target %s: %w", existing.TargetPath, err)
		}
	}

	if err := writeFileAtomic(write.targetPath, []byte(write.content)); err != nil {
		return false, false, err
	}
	return true, true, nil
}

func (s *Syncer) resolveWriteConflicts(writes []fileWrite) ([]fileWrite, map[string]struct{}) {
	if len(writes) <= 1 {
		activeTargets := make(map[string]struct{}, len(writes))
		for _, write := range writes {
			activeTargets[write.targetPath] = struct{}{}
		}
		return writes, activeTargets
	}

	grouped := make(map[string][]fileWrite)
	order := make([]string, 0, len(writes))
	for _, write := range writes {
		if _, ok := grouped[write.targetPath]; !ok {
			order = append(order, write.targetPath)
		}
		grouped[write.targetPath] = append(grouped[write.targetPath], write)
	}

	resolved := make([]fileWrite, 0, len(order))
	activeTargets := make(map[string]struct{}, len(order))
	for _, targetPath := range order {
		group := grouped[targetPath]
		best := group[0]
		if len(group) > 1 {
			for _, candidate := range group[1:] {
				if preferWrite(candidate, best) {
					best = candidate
				}
			}

			names := make([]string, 0, len(group))
			for _, candidate := range group {
				names = append(names, candidate.name)
			}
			s.warnf("multiple source files map to one strm target=%s chosen=%s candidates=%s", targetPath, best.name, strings.Join(names, ", "))
		}
		resolved = append(resolved, best)
		activeTargets[targetPath] = struct{}{}
	}

	return resolved, activeTargets
}

func preferWrite(candidate, current fileWrite) bool {
	if candidate.size != current.size {
		return candidate.size > current.size
	}

	candidatePriority := extPriority(filepath.Ext(candidate.name))
	currentPriority := extPriority(filepath.Ext(current.name))
	if candidatePriority != currentPriority {
		return candidatePriority < currentPriority
	}

	return candidate.sourcePath < current.sourcePath
}

func extPriority(ext string) int {
	switch strings.ToLower(ext) {
	case ".mkv":
		return 0
	case ".mp4":
		return 1
	case ".ts":
		return 2
	case ".m2ts":
		return 3
	case ".avi":
		return 4
	case ".mov":
		return 5
	case ".wmv":
		return 6
	case ".flv":
		return 7
	case ".mpg", ".mpeg":
		return 8
	default:
		return 99
	}
}

func (s *Syncer) isVideo(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := s.cfg.Sync.VideoExts[ext]
	return ok
}

func (s *Syncer) targetPathFor(rule config.Rule, sourcePath string) string {
	rel := relativeSourcePath(rule.SourcePath, sourcePath)
	ext := filepath.Ext(rel)
	if rule.FlattenValue() {
		name := filepath.Base(rel)
		name = strings.TrimSuffix(name, ext) + ".strm"
		return filepath.Join(rule.TargetPath, name)
	}
	return filepath.Join(rule.TargetPath, filepath.FromSlash(strings.TrimSuffix(rel, ext)+".strm"))
}

func (s *Syncer) targetDirFor(rule config.Rule, sourceDir string) string {
	if rule.FlattenValue() {
		return rule.TargetPath
	}
	rel := relativeSourcePath(rule.SourcePath, sourceDir)
	if rel == "" {
		return rule.TargetPath
	}
	return filepath.Join(rule.TargetPath, filepath.FromSlash(rel))
}

func (s *Syncer) removeEmptyParents(start, stop string) {
	current := filepath.Clean(start)
	stop = filepath.Clean(stop)
	for current != stop && current != string(os.PathSeparator) && current != "." {
		if err := os.Remove(current); err != nil {
			return
		}
		current = filepath.Dir(current)
	}
}

func writeFileAtomic(target string, content []byte) error {
	tempFile, err := os.CreateTemp(filepath.Dir(target), ".emby-pro-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(content); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
}

func joinSourcePath(parent, child string) string {
	if parent == "/" {
		return path.Clean("/" + child)
	}
	return path.Clean(path.Join(parent, child))
}

func relativeSourcePath(root, full string) string {
	root = path.Clean(root)
	full = path.Clean(full)
	if root == "/" {
		return strings.TrimPrefix(full, "/")
	}
	rel := strings.TrimPrefix(full, root)
	return strings.TrimPrefix(rel, "/")
}

func (s *Syncer) debugf(format string, args ...any) {
	if s.cfg.Sync.LogLevel == "debug" {
		s.logger.Printf("[DEBUG] "+format, args...)
	}
}

func (s *Syncer) infof(format string, args ...any) {
	s.logger.Printf("[INFO] "+format, args...)
}

func (s *Syncer) warnf(format string, args ...any) {
	s.logger.Printf("[WARN] "+format, args...)
}

func (s *Syncer) cleanupStaleRules() error {
	states, err := s.store.ListRuleStates()
	if err != nil {
		return fmt.Errorf("list stored rules: %w", err)
	}

	activeRules := make(map[string]struct{}, len(s.cfg.Rules))
	for _, rule := range s.cfg.Rules {
		activeRules[rule.Name] = struct{}{}
	}

	for _, state := range states {
		if _, ok := activeRules[state.RuleName]; ok {
			continue
		}

		files, err := s.store.ListFilesByRule(state.RuleName)
		if err != nil {
			return fmt.Errorf("list files for stale rule %s: %w", state.RuleName, err)
		}

		cleaned := 0
		for _, file := range files {
			inUse, err := s.store.OtherRuleUsesTarget(state.RuleName, file.TargetPath)
			if err != nil {
				return fmt.Errorf("check target usage for stale rule %s: %w", state.RuleName, err)
			}
			if inUse {
				continue
			}
			if err := os.Remove(file.TargetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.warnf("remove stale file %s: %v", file.TargetPath, err)
				continue
			}
			cleaned++
			if state.TargetRoot != "" {
				s.removeEmptyParents(filepath.Dir(file.TargetPath), state.TargetRoot)
			}
		}

		if err := s.store.DeleteRule(state.RuleName); err != nil {
			return fmt.Errorf("delete stale rule %s: %w", state.RuleName, err)
		}
		s.infof("removed stale rule=%s cleaned_files=%d", state.RuleName, cleaned)
	}

	return nil
}
