package syncer

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
)

const (
	dirResultChanged      = "changed"
	dirResultUnchanged    = "unchanged"
	dirResultFailed       = "failed"
	dirResultRuleCooldown = "rule_cooldown"
)

type listingState struct {
	entryCount     int
	latestModified time.Time
}

func summarizeEntries(entries []openlist.Entry) listingState {
	state := listingState{
		entryCount: len(entries),
	}
	for _, entry := range entries {
		if entry.Modified.After(state.latestModified) {
			state.latestModified = entry.Modified
		}
	}
	return state
}

func (s *Syncer) nextSuccessSchedule(dir index.DirRecord, scheduleChanged bool, now time.Time) (time.Time, int, string) {
	if scheduleChanged || dir.LastResult == "" {
		return s.scheduleWithJitter(now, s.cfg.Sync.HotInterval, s.cfg.Sync.HotJitter), 0, dirResultChanged
	}

	unchangedStreak := dir.UnchangedStreak + 1
	interval := s.cfg.Sync.HotInterval
	jitter := s.cfg.Sync.HotJitter
	switch {
	case unchangedStreak >= s.cfg.Sync.UnchangedToCold:
		interval = s.cfg.Sync.ColdInterval
		jitter = s.cfg.Sync.ColdJitter
	case unchangedStreak >= s.cfg.Sync.UnchangedToWarm:
		interval = s.cfg.Sync.WarmInterval
		jitter = s.cfg.Sync.WarmJitter
	}
	return s.scheduleWithJitter(now, interval, jitter), unchangedStreak, dirResultUnchanged
}

func hasScheduleChange(existingFiles []index.FileRecord, existingDirs []string, writes []fileWrite, seenDirs map[string]struct{}) bool {
	existingDirSet := make(map[string]struct{}, len(existingDirs))
	for _, existingDir := range existingDirs {
		existingDirSet[existingDir] = struct{}{}
	}
	for seenDir := range seenDirs {
		if _, ok := existingDirSet[seenDir]; !ok {
			return true
		}
		delete(existingDirSet, seenDir)
	}
	if len(existingDirSet) > 0 {
		return true
	}

	existingFileMap := make(map[string]index.FileRecord, len(existingFiles))
	for _, existingFile := range existingFiles {
		existingFileMap[existingFile.SourcePath] = existingFile
	}
	for _, write := range writes {
		existingFile, ok := existingFileMap[write.sourcePath]
		if !ok {
			return true
		}
		if existingFile.TargetPath != write.targetPath || existingFile.ContentHash != write.contentHash {
			return true
		}
		delete(existingFileMap, write.sourcePath)
	}
	return len(existingFileMap) > 0
}

func (s *Syncer) nextFailureSchedule(dir index.DirRecord, now time.Time, err error) (time.Time, time.Time, string) {
	if isWindControlError(err) {
		cooldownUntil := now.Add(s.cfg.Sync.RuleCooldown)
		return cooldownUntil, cooldownUntil, dirResultRuleCooldown
	}

	attempt := dir.FailCount + 1
	backoff := failureBackoff(attempt)
	if backoff > s.cfg.Sync.FailureBackoffMax {
		backoff = s.cfg.Sync.FailureBackoffMax
	}
	nextRetry := now.Add(backoff)
	return nextRetry, nextRetry, dirResultFailed
}

func failureBackoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 30 * time.Minute
	case attempt == 2:
		return 2 * time.Hour
	case attempt == 3:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func isWindControlError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, token := range []string{
		" 429",
		"status 429",
		"too many requests",
		"rate limit",
		"wind control",
		"risk control",
		"风控",
		"限流",
	} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

func (s *Syncer) scheduleWithJitter(now time.Time, base, jitter time.Duration) time.Time {
	if jitter <= 0 {
		return now.Add(base)
	}
	return now.Add(base + time.Duration(s.rng.Int63n(int64(jitter)+1)))
}
