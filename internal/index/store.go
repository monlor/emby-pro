package index

import (
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/monlor/emby-pro/internal/config"
)

type Store struct {
	db *sql.DB
}

type DirRecord struct {
	RuleName        string
	SourcePath      string
	ParentPath      string
	Depth           int
	NextScanAt      time.Time
	LastScanAt      time.Time
	LastSuccessAt   time.Time
	FailCount       int
	LastError       string
	UnchangedStreak int
	LastRemoteMtime time.Time
	LastEntryCount  int
	CooldownUntil   time.Time
	LastResult      string
	RuleSeed        int64
}

type FileRecord struct {
	RuleName    string
	SourcePath  string
	ParentPath  string
	TargetPath  string
	ContentHash string
	LastSeenAt  time.Time
}

type RuleState struct {
	RuleName                  string
	RootPath                  string
	TargetRoot                string
	LastFullRescanAt          time.Time
	LastFullRescanCompletedAt time.Time
	FullRescanActive          bool
	PendingFullRescan         bool
	ScheduleSeed              int64
	CooldownUntil             time.Time
}

func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	stmts := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS dirs (
			rule_name TEXT NOT NULL,
			source_path TEXT NOT NULL,
			parent_path TEXT NOT NULL,
			depth INTEGER NOT NULL,
			next_scan_at INTEGER NOT NULL,
			last_scan_at INTEGER NOT NULL DEFAULT 0,
			last_success_at INTEGER NOT NULL DEFAULT 0,
			fail_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			unchanged_streak INTEGER NOT NULL DEFAULT 0,
			last_remote_mtime INTEGER NOT NULL DEFAULT 0,
			last_entry_count INTEGER NOT NULL DEFAULT 0,
			cooldown_until INTEGER NOT NULL DEFAULT 0,
			last_result TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(rule_name, source_path)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_dirs_next_scan ON dirs(next_scan_at);`,
		`CREATE INDEX IF NOT EXISTS idx_dirs_parent ON dirs(rule_name, parent_path);`,
		`CREATE TABLE IF NOT EXISTS files (
			rule_name TEXT NOT NULL,
			source_path TEXT NOT NULL,
			parent_path TEXT NOT NULL,
			target_path TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			last_seen_at INTEGER NOT NULL,
			PRIMARY KEY(rule_name, source_path)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_files_parent ON files(rule_name, parent_path);`,
		`CREATE INDEX IF NOT EXISTS idx_files_target ON files(target_path);`,
		`CREATE TABLE IF NOT EXISTS rule_state (
				rule_name TEXT PRIMARY KEY,
				root_path TEXT NOT NULL,
				target_root TEXT NOT NULL DEFAULT '',
				last_full_rescan_at INTEGER NOT NULL DEFAULT 0,
				last_full_rescan_completed_at INTEGER NOT NULL DEFAULT 0,
				full_rescan_active INTEGER NOT NULL DEFAULT 0,
				pending_full_rescan INTEGER NOT NULL DEFAULT 0,
				schedule_seed INTEGER NOT NULL DEFAULT 0,
				cooldown_until INTEGER NOT NULL DEFAULT 0
			);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("init sqlite schema: %w", err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE rule_state ADD COLUMN target_root TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		db.Close()
		return nil, fmt.Errorf("migrate rule_state schema: %w", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE dirs ADD COLUMN unchanged_streak INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE dirs ADD COLUMN last_remote_mtime INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE dirs ADD COLUMN last_entry_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE dirs ADD COLUMN cooldown_until INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE dirs ADD COLUMN last_result TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE rule_state ADD COLUMN last_full_rescan_completed_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE rule_state ADD COLUMN full_rescan_active INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE rule_state ADD COLUMN pending_full_rescan INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE rule_state ADD COLUMN schedule_seed INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE rule_state ADD COLUMN cooldown_until INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate sqlite schema: %w", err)
		}
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureRule(rule config.Rule, now time.Time) error {
	seed, err := randomSeed()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO rule_state(rule_name, root_path, target_root, last_full_rescan_at, last_full_rescan_completed_at, full_rescan_active, pending_full_rescan, schedule_seed, cooldown_until)
		 VALUES (?, ?, ?, 0, 0, 0, 0, ?, 0)
		 ON CONFLICT(rule_name) DO UPDATE SET
		   root_path = excluded.root_path,
		   target_root = excluded.target_root,
		   schedule_seed = CASE WHEN rule_state.schedule_seed = 0 THEN excluded.schedule_seed ELSE rule_state.schedule_seed END`,
		rule.Name, rule.SourcePath, rule.TargetPath, seed,
	); err != nil {
		return err
	}

	if _, err := tx.Exec(
		`INSERT INTO dirs(rule_name, source_path, parent_path, depth, next_scan_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(rule_name, source_path) DO NOTHING`,
		rule.Name, rule.SourcePath, "", 0, now.Unix(),
	); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) DueDirs(limit int, now time.Time) ([]DirRecord, error) {
	candidateLimit := max(limit*4, 64)
	if limit <= 0 {
		candidateLimit = 64
	}
	rows, err := s.db.Query(
		`SELECT d.rule_name, d.source_path, d.parent_path, d.depth, d.next_scan_at, d.last_scan_at, d.last_success_at, d.fail_count, d.last_error,
		        d.unchanged_streak, d.last_remote_mtime, d.last_entry_count, d.cooldown_until, d.last_result, rs.schedule_seed
		   FROM dirs d
		   JOIN rule_state rs ON rs.rule_name = d.rule_name
		  WHERE d.next_scan_at <= ?
		    AND d.cooldown_until <= ?
		    AND rs.cooldown_until <= ?
		  ORDER BY d.next_scan_at ASC, d.depth ASC, d.last_success_at ASC, d.source_path ASC
		  LIMIT ?`,
		now.Unix(), now.Unix(), now.Unix(), candidateLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dirs []DirRecord
	for rows.Next() {
		var rec DirRecord
		var nextScanAt, lastScanAt, lastSuccessAt, lastRemoteMtime, cooldownUntil int64
		if err := rows.Scan(
			&rec.RuleName,
			&rec.SourcePath,
			&rec.ParentPath,
			&rec.Depth,
			&nextScanAt,
			&lastScanAt,
			&lastSuccessAt,
			&rec.FailCount,
			&rec.LastError,
			&rec.UnchangedStreak,
			&lastRemoteMtime,
			&rec.LastEntryCount,
			&cooldownUntil,
			&rec.LastResult,
			&rec.RuleSeed,
		); err != nil {
			return nil, err
		}
		rec.NextScanAt = unixToTime(nextScanAt)
		rec.LastScanAt = unixToTime(lastScanAt)
		rec.LastSuccessAt = unixToTime(lastSuccessAt)
		rec.LastRemoteMtime = unixToTime(lastRemoteMtime)
		rec.CooldownUntil = unixToTime(cooldownUntil)
		dirs = append(dirs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sortDueDirs(dirs)
	if limit > 0 && len(dirs) > limit {
		dirs = dirs[:limit]
	}
	return dirs, nil
}

func (s *Store) NextEligibleScanAt() (time.Time, error) {
	row := s.db.QueryRow(
		`SELECT COALESCE(MIN(max(d.next_scan_at, d.cooldown_until, rs.cooldown_until)), 0)
		   FROM dirs d
		   JOIN rule_state rs ON rs.rule_name = d.rule_name`,
	)
	var nextAt int64
	if err := row.Scan(&nextAt); err != nil {
		return time.Time{}, err
	}
	return unixToTime(nextAt), nil
}

func (s *Store) GetDir(ruleName, sourcePath string) (DirRecord, error) {
	row := s.db.QueryRow(
		`SELECT d.rule_name, d.source_path, d.parent_path, d.depth, d.next_scan_at, d.last_scan_at, d.last_success_at, d.fail_count, d.last_error,
		        d.unchanged_streak, d.last_remote_mtime, d.last_entry_count, d.cooldown_until, d.last_result, rs.schedule_seed
		   FROM dirs d
		   JOIN rule_state rs ON rs.rule_name = d.rule_name
		  WHERE d.rule_name = ? AND d.source_path = ?`,
		ruleName, sourcePath,
	)

	var rec DirRecord
	var nextScanAt, lastScanAt, lastSuccessAt, lastRemoteMtime, cooldownUntil int64
	if err := row.Scan(
		&rec.RuleName,
		&rec.SourcePath,
		&rec.ParentPath,
		&rec.Depth,
		&nextScanAt,
		&lastScanAt,
		&lastSuccessAt,
		&rec.FailCount,
		&rec.LastError,
		&rec.UnchangedStreak,
		&lastRemoteMtime,
		&rec.LastEntryCount,
		&cooldownUntil,
		&rec.LastResult,
		&rec.RuleSeed,
	); err != nil {
		return DirRecord{}, err
	}

	rec.NextScanAt = unixToTime(nextScanAt)
	rec.LastScanAt = unixToTime(lastScanAt)
	rec.LastSuccessAt = unixToTime(lastSuccessAt)
	rec.LastRemoteMtime = unixToTime(lastRemoteMtime)
	rec.CooldownUntil = unixToTime(cooldownUntil)
	return rec, nil
}

func (s *Store) UpsertDir(ruleName, sourcePath, parentPath string, depth int, nextScanAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO dirs(rule_name, source_path, parent_path, depth, next_scan_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(rule_name, source_path) DO UPDATE SET
		   parent_path = excluded.parent_path,
		   depth = excluded.depth,
		   next_scan_at = MIN(dirs.next_scan_at, excluded.next_scan_at)`,
		ruleName, sourcePath, parentPath, depth, nextScanAt.Unix(),
	)
	return err
}

func (s *Store) MarkDirScannedSuccess(ruleName, sourcePath string, nextScanAt, remoteMtime time.Time, entryCount int, lastResult string, unchangedStreak int, now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE dirs
		    SET next_scan_at = ?, last_scan_at = ?, last_success_at = ?, fail_count = 0, last_error = '',
		        unchanged_streak = ?, last_remote_mtime = ?, last_entry_count = ?, cooldown_until = 0, last_result = ?
		  WHERE rule_name = ? AND source_path = ?`,
		nextScanAt.Unix(), now.Unix(), now.Unix(), unchangedStreak, remoteMtime.Unix(), entryCount, lastResult, ruleName, sourcePath,
	)
	return err
}

func (s *Store) MarkDirScannedFailed(ruleName, sourcePath, lastError string, nextScanAt, cooldownUntil, now time.Time, lastResult string) error {
	_, err := s.db.Exec(
		`UPDATE dirs
		    SET next_scan_at = ?, last_scan_at = ?, fail_count = fail_count + 1, last_error = ?,
		        cooldown_until = ?, last_result = ?
		  WHERE rule_name = ? AND source_path = ?`,
		nextScanAt.Unix(), now.Unix(), lastError, cooldownUntil.Unix(), lastResult, ruleName, sourcePath,
	)
	return err
}

func (s *Store) ListChildDirs(ruleName, parentPath string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT source_path FROM dirs WHERE rule_name = ? AND parent_path = ? ORDER BY source_path ASC`,
		ruleName, parentPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var sourcePath string
		if err := rows.Scan(&sourcePath); err != nil {
			return nil, err
		}
		result = append(result, sourcePath)
	}
	return result, rows.Err()
}

func (s *Store) ListFilesByParent(ruleName, parentPath string) ([]FileRecord, error) {
	rows, err := s.db.Query(
		`SELECT rule_name, source_path, parent_path, target_path, content_hash, last_seen_at
		   FROM files
		  WHERE rule_name = ? AND parent_path = ?
		  ORDER BY source_path ASC`,
		ruleName, parentPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []FileRecord
	for rows.Next() {
		var rec FileRecord
		var lastSeenAt int64
		if err := rows.Scan(
			&rec.RuleName,
			&rec.SourcePath,
			&rec.ParentPath,
			&rec.TargetPath,
			&rec.ContentHash,
			&lastSeenAt,
		); err != nil {
			return nil, err
		}
		rec.LastSeenAt = unixToTime(lastSeenAt)
		result = append(result, rec)
	}
	return result, rows.Err()
}

func (s *Store) ListFilesByRule(ruleName string) ([]FileRecord, error) {
	rows, err := s.db.Query(
		`SELECT rule_name, source_path, parent_path, target_path, content_hash, last_seen_at
		   FROM files
		  WHERE rule_name = ?
		  ORDER BY source_path ASC`,
		ruleName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []FileRecord
	for rows.Next() {
		var rec FileRecord
		var lastSeenAt int64
		if err := rows.Scan(
			&rec.RuleName,
			&rec.SourcePath,
			&rec.ParentPath,
			&rec.TargetPath,
			&rec.ContentHash,
			&lastSeenAt,
		); err != nil {
			return nil, err
		}
		rec.LastSeenAt = unixToTime(lastSeenAt)
		result = append(result, rec)
	}
	return result, rows.Err()
}

func (s *Store) UpsertFile(rec FileRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO files(rule_name, source_path, parent_path, target_path, content_hash, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(rule_name, source_path) DO UPDATE SET
		   parent_path = excluded.parent_path,
		   target_path = excluded.target_path,
		   content_hash = excluded.content_hash,
		   last_seen_at = excluded.last_seen_at`,
		rec.RuleName, rec.SourcePath, rec.ParentPath, rec.TargetPath, rec.ContentHash, rec.LastSeenAt.Unix(),
	)
	return err
}

func (s *Store) DeleteFile(ruleName, sourcePath string) error {
	_, err := s.db.Exec(`DELETE FROM files WHERE rule_name = ? AND source_path = ?`, ruleName, sourcePath)
	return err
}

func (s *Store) DeleteDirSubtree(ruleName, dirPath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	prefix := dirPath + "/%"
	if _, err := tx.Exec(
		`DELETE FROM files WHERE rule_name = ? AND (source_path = ? OR source_path LIKE ?)`,
		ruleName, dirPath, prefix,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`DELETE FROM dirs WHERE rule_name = ? AND (source_path = ? OR source_path LIKE ?)`,
		ruleName, dirPath, prefix,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListRuleStates() ([]RuleState, error) {
	rows, err := s.db.Query(`SELECT rule_name, root_path, target_root, last_full_rescan_at, last_full_rescan_completed_at, full_rescan_active, pending_full_rescan, schedule_seed, cooldown_until FROM rule_state ORDER BY rule_name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []RuleState
	for rows.Next() {
		var state RuleState
		var lastFullRescanAt, lastFullRescanCompletedAt, cooldownUntil int64
		var fullRescanActive, pendingFullRescan int
		if err := rows.Scan(&state.RuleName, &state.RootPath, &state.TargetRoot, &lastFullRescanAt, &lastFullRescanCompletedAt, &fullRescanActive, &pendingFullRescan, &state.ScheduleSeed, &cooldownUntil); err != nil {
			return nil, err
		}
		state.LastFullRescanAt = unixToTime(lastFullRescanAt)
		state.LastFullRescanCompletedAt = unixToTime(lastFullRescanCompletedAt)
		state.FullRescanActive = fullRescanActive != 0
		state.PendingFullRescan = pendingFullRescan != 0
		state.CooldownUntil = unixToTime(cooldownUntil)
		states = append(states, state)
	}
	return states, rows.Err()
}

func (s *Store) OtherRuleUsesTarget(ruleName, targetPath string) (bool, error) {
	row := s.db.QueryRow(`SELECT COUNT(1) FROM files WHERE target_path = ? AND rule_name != ?`, targetPath, ruleName)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) TargetInUseByAnotherSource(ruleName, sourcePath, targetPath string) (bool, error) {
	row := s.db.QueryRow(
		`SELECT COUNT(1)
		   FROM files
		  WHERE target_path = ?
		    AND NOT (rule_name = ? AND source_path = ?)`,
		targetPath, ruleName, sourcePath,
	)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) DeleteRule(ruleName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`DELETE FROM files WHERE rule_name = ?`,
		`DELETE FROM dirs WHERE rule_name = ?`,
		`DELETE FROM rule_state WHERE rule_name = ?`,
	} {
		if _, err := tx.Exec(stmt, ruleName); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) ShouldScheduleFullRescan(ruleName string, interval time.Duration, now time.Time) (bool, error) {
	row := s.db.QueryRow(`SELECT last_full_rescan_at, last_full_rescan_completed_at FROM rule_state WHERE rule_name = ?`, ruleName)
	var lastFull, lastCompleted int64
	if err := row.Scan(&lastFull, &lastCompleted); err != nil {
		return false, err
	}
	referenceAt := lastFull
	if lastCompleted > 0 {
		referenceAt = lastCompleted
	}
	if referenceAt == 0 {
		return true, nil
	}
	return now.Sub(time.Unix(referenceAt, 0)) >= interval, nil
}

func (s *Store) ScheduleFullRescan(ruleName string, now time.Time) error {
	_, _, err := s.RequestFullRescan(ruleName, now)
	return err
}

func (s *Store) RequestFullRescan(ruleName string, now time.Time) (scheduled bool, pending bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()

	var rootPath string
	var fullRescanActive, pendingFullRescan int
	if err := tx.QueryRow(`SELECT root_path, full_rescan_active, pending_full_rescan FROM rule_state WHERE rule_name = ?`, ruleName).Scan(&rootPath, &fullRescanActive, &pendingFullRescan); err != nil {
		return false, false, err
	}

	if fullRescanActive != 0 {
		if pendingFullRescan != 0 {
			return false, false, tx.Commit()
		}
		if _, err := tx.Exec(`UPDATE rule_state SET pending_full_rescan = 1 WHERE rule_name = ?`, ruleName); err != nil {
			return false, false, err
		}
		return false, true, tx.Commit()
	}

	if _, err := tx.Exec(`UPDATE rule_state SET last_full_rescan_at = ?, full_rescan_active = 1, pending_full_rescan = 0 WHERE rule_name = ?`, now.Unix(), ruleName); err != nil {
		return false, false, err
	}
	if _, err := tx.Exec(
		`UPDATE dirs
		    SET next_scan_at = MIN(next_scan_at, ?)
		  WHERE rule_name = ?
		    AND source_path = ?`,
		now.Unix(), ruleName, rootPath,
	); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (s *Store) AdvanceFullRescan(ruleName string, now time.Time) (completed bool, startedPending bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()

	var rootPath string
	var lastFullRescanAt int64
	var fullRescanActive, pendingFullRescan int
	if err := tx.QueryRow(
		`SELECT root_path, last_full_rescan_at, full_rescan_active, pending_full_rescan
		   FROM rule_state
		  WHERE rule_name = ?`,
		ruleName,
	).Scan(&rootPath, &lastFullRescanAt, &fullRescanActive, &pendingFullRescan); err != nil {
		return false, false, err
	}
	if fullRescanActive == 0 || lastFullRescanAt == 0 {
		return false, false, tx.Commit()
	}

	var remaining int
	if err := tx.QueryRow(
		`SELECT COUNT(1)
		   FROM dirs
		  WHERE rule_name = ?
		    AND last_scan_at < ?`,
		ruleName, lastFullRescanAt,
	).Scan(&remaining); err != nil {
		return false, false, err
	}
	if remaining > 0 {
		return false, false, tx.Commit()
	}

	if pendingFullRescan != 0 {
		if _, err := tx.Exec(
			`UPDATE rule_state
			    SET last_full_rescan_at = ?, last_full_rescan_completed_at = ?, pending_full_rescan = 0, full_rescan_active = 1
			  WHERE rule_name = ?`,
			now.Unix(), now.Unix(), ruleName,
		); err != nil {
			return false, false, err
		}
		if _, err := tx.Exec(
			`UPDATE dirs
			    SET next_scan_at = MIN(next_scan_at, ?)
			  WHERE rule_name = ?
			    AND source_path = ?`,
			now.Unix(), ruleName, rootPath,
		); err != nil {
			return false, false, err
		}
		if err := tx.Commit(); err != nil {
			return false, false, err
		}
		return true, true, nil
	}

	if _, err := tx.Exec(
		`UPDATE rule_state
		    SET full_rescan_active = 0, last_full_rescan_completed_at = ?
		  WHERE rule_name = ?`,
		now.Unix(), ruleName,
	); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (s *Store) SetRuleCooldown(ruleName string, cooldownUntil time.Time) error {
	_, err := s.db.Exec(`UPDATE rule_state SET cooldown_until = ? WHERE rule_name = ?`, cooldownUntil.Unix(), ruleName)
	return err
}

func sortDueDirs(dirs []DirRecord) {
	sort.SliceStable(dirs, func(i, j int) bool {
		if !dirs[i].NextScanAt.Equal(dirs[j].NextScanAt) {
			return dirs[i].NextScanAt.Before(dirs[j].NextScanAt)
		}
		if dirs[i].Depth != dirs[j].Depth {
			return dirs[i].Depth < dirs[j].Depth
		}
		if !dirs[i].LastSuccessAt.Equal(dirs[j].LastSuccessAt) {
			return dirs[i].LastSuccessAt.Before(dirs[j].LastSuccessAt)
		}
		left := dueOrderKey(dirs[i].RuleSeed, dirs[i].RuleName, dirs[i].SourcePath)
		right := dueOrderKey(dirs[j].RuleSeed, dirs[j].RuleName, dirs[j].SourcePath)
		if left != right {
			return left < right
		}
		return dirs[i].SourcePath < dirs[j].SourcePath
	})
}

func dueOrderKey(seed int64, ruleName, sourcePath string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(ruleName))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(sourcePath))
	_, _ = hasher.Write([]byte{0})
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(seed))
	_, _ = hasher.Write(buf[:])
	return hasher.Sum64()
}

func randomSeed() (int64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	seed := int64(binary.LittleEndian.Uint64(buf[:]))
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return seed, nil
}

func unixToTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0)
}
