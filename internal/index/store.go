package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/monlor/emby-pro/internal/config"
)

type Store struct {
	db *sql.DB
}

type DirRecord struct {
	RuleName      string
	SourcePath    string
	ParentPath    string
	Depth         int
	NextScanAt    time.Time
	LastScanAt    time.Time
	LastSuccessAt time.Time
	FailCount     int
	LastError     string
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
	RuleName   string
	RootPath   string
	TargetRoot string
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
				last_full_rescan_at INTEGER NOT NULL DEFAULT 0
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

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureRule(rule config.Rule, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO rule_state(rule_name, root_path, target_root, last_full_rescan_at)
		 VALUES (?, ?, ?, 0)
		 ON CONFLICT(rule_name) DO UPDATE SET root_path = excluded.root_path, target_root = excluded.target_root`,
		rule.Name, rule.SourcePath, rule.TargetPath,
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
	rows, err := s.db.Query(
		`SELECT rule_name, source_path, parent_path, depth, next_scan_at, last_scan_at, last_success_at, fail_count, last_error
		   FROM dirs
		  WHERE next_scan_at <= ?
		  ORDER BY depth ASC, last_success_at ASC, source_path ASC
		  LIMIT ?`,
		now.Unix(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dirs []DirRecord
	for rows.Next() {
		var rec DirRecord
		var nextScanAt, lastScanAt, lastSuccessAt int64
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
		); err != nil {
			return nil, err
		}
		rec.NextScanAt = unixToTime(nextScanAt)
		rec.LastScanAt = unixToTime(lastScanAt)
		rec.LastSuccessAt = unixToTime(lastSuccessAt)
		dirs = append(dirs, rec)
	}
	return dirs, rows.Err()
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

func (s *Store) MarkDirScannedSuccess(ruleName, sourcePath string, nextScanAt, now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE dirs
		    SET next_scan_at = ?, last_scan_at = ?, last_success_at = ?, fail_count = 0, last_error = ''
		  WHERE rule_name = ? AND source_path = ?`,
		nextScanAt.Unix(), now.Unix(), now.Unix(), ruleName, sourcePath,
	)
	return err
}

func (s *Store) MarkDirScannedFailed(ruleName, sourcePath, lastError string, nextScanAt, now time.Time) error {
	_, err := s.db.Exec(
		`UPDATE dirs
		    SET next_scan_at = ?, last_scan_at = ?, fail_count = fail_count + 1, last_error = ?
		  WHERE rule_name = ? AND source_path = ?`,
		nextScanAt.Unix(), now.Unix(), lastError, ruleName, sourcePath,
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
	rows, err := s.db.Query(`SELECT rule_name, root_path, target_root FROM rule_state ORDER BY rule_name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []RuleState
	for rows.Next() {
		var state RuleState
		if err := rows.Scan(&state.RuleName, &state.RootPath, &state.TargetRoot); err != nil {
			return nil, err
		}
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
	row := s.db.QueryRow(`SELECT last_full_rescan_at FROM rule_state WHERE rule_name = ?`, ruleName)
	var lastFull int64
	if err := row.Scan(&lastFull); err != nil {
		return false, err
	}
	if lastFull == 0 {
		return true, nil
	}
	return now.Sub(time.Unix(lastFull, 0)) >= interval, nil
}

func (s *Store) ScheduleFullRescan(ruleName string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE rule_state SET last_full_rescan_at = ? WHERE rule_name = ?`, now.Unix(), ruleName); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE dirs SET next_scan_at = MIN(next_scan_at, ?) WHERE rule_name = ?`, now.Unix(), ruleName); err != nil {
		return err
	}
	return tx.Commit()
}

func unixToTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0)
}
