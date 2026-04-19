package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
)

func TestScheduleFullRescanOnlyQueuesRoot(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	rule := config.Rule{Name: "media", SourcePath: "/media", TargetPath: "/strm/media"}
	if err := store.EnsureRule(rule, now); err != nil {
		t.Fatalf("EnsureRule() error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE dirs SET next_scan_at = ? WHERE rule_name = ?`, now.Add(24*time.Hour).Unix(), rule.Name); err != nil {
		t.Fatalf("update root next_scan_at: %v", err)
	}
	if err := store.UpsertDir(rule.Name, "/media/movies", "/media", 1, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("UpsertDir() error = %v", err)
	}

	if err := store.ScheduleFullRescan(rule.Name, now); err != nil {
		t.Fatalf("ScheduleFullRescan() error = %v", err)
	}

	dirs, err := store.DueDirs(10, now)
	if err != nil {
		t.Fatalf("DueDirs() error = %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected only root to be due, got %d", len(dirs))
	}
	if got, want := dirs[0].SourcePath, "/media"; got != want {
		t.Fatalf("due dir = %s, want %s", got, want)
	}
}

func TestDueDirsHonorsRuleCooldownAndSeedOrder(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	rule := config.Rule{Name: "media", SourcePath: "/media", TargetPath: "/strm/media"}
	if err := store.EnsureRule(rule, now); err != nil {
		t.Fatalf("EnsureRule() error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE dirs SET next_scan_at = ? WHERE rule_name = ? AND source_path = ?`, now.Add(24*time.Hour).Unix(), rule.Name, rule.SourcePath); err != nil {
		t.Fatalf("update root next_scan_at: %v", err)
	}

	seed := int64(1)
	for ; seed < 512; seed++ {
		if dueOrderKey(seed, rule.Name, "/media/a") > dueOrderKey(seed, rule.Name, "/media/b") {
			break
		}
	}
	if seed == 512 {
		t.Fatalf("failed to find non-lexical test seed")
	}
	if _, err := store.db.Exec(`UPDATE rule_state SET schedule_seed = ? WHERE rule_name = ?`, seed, rule.Name); err != nil {
		t.Fatalf("update schedule seed: %v", err)
	}

	for _, dir := range []string{"/media/a", "/media/b"} {
		if err := store.UpsertDir(rule.Name, dir, "/media", 1, now); err != nil {
			t.Fatalf("UpsertDir(%s) error = %v", dir, err)
		}
	}
	if _, err := store.db.Exec(`UPDATE dirs SET last_success_at = 0 WHERE rule_name = ? AND source_path IN (?, ?)`, rule.Name, "/media/a", "/media/b"); err != nil {
		t.Fatalf("update last_success_at: %v", err)
	}

	dirs, err := store.DueDirs(10, now)
	if err != nil {
		t.Fatalf("DueDirs() error = %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 due dirs, got %d", len(dirs))
	}
	if got, want := dirs[0].SourcePath, "/media/b"; got != want {
		t.Fatalf("seed order first dir = %s, want %s", got, want)
	}

	if err := store.SetRuleCooldown(rule.Name, now.Add(time.Hour)); err != nil {
		t.Fatalf("SetRuleCooldown() error = %v", err)
	}
	dirs, err = store.DueDirs(10, now)
	if err != nil {
		t.Fatalf("DueDirs() with cooldown error = %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("expected cooldown to hide due dirs, got %d", len(dirs))
	}
}

func TestDueDirsUsesBoundedCandidateWindow(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	rule := config.Rule{Name: "media", SourcePath: "/media", TargetPath: "/strm/media"}
	if err := store.EnsureRule(rule, now); err != nil {
		t.Fatalf("EnsureRule() error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE dirs SET next_scan_at = ? WHERE rule_name = ? AND source_path = ?`, now.Add(24*time.Hour).Unix(), rule.Name, rule.SourcePath); err != nil {
		t.Fatalf("update root next_scan_at: %v", err)
	}

	for i := 0; i < 80; i++ {
		dir := filepath.ToSlash(filepath.Join("/media", fmt.Sprintf("dir-%03d", i)))
		if err := store.UpsertDir(rule.Name, dir, "/media", 1, now); err != nil {
			t.Fatalf("UpsertDir(%s) error = %v", dir, err)
		}
	}

	dirs, err := store.DueDirs(1, now)
	if err != nil {
		t.Fatalf("DueDirs() error = %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 due dir, got %d", len(dirs))
	}
}

func TestNextEligibleScanAtUsesCooldownBoundary(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	rule := config.Rule{Name: "media", SourcePath: "/media", TargetPath: "/strm/media"}
	if err := store.EnsureRule(rule, now); err != nil {
		t.Fatalf("EnsureRule() error = %v", err)
	}

	nextScanAt := now.Add(5 * time.Second)
	ruleCooldown := now.Add(20 * time.Second)
	if _, err := store.db.Exec(`UPDATE dirs SET next_scan_at = ?, cooldown_until = ? WHERE rule_name = ? AND source_path = ?`, nextScanAt.Unix(), now.Add(10*time.Second).Unix(), rule.Name, rule.SourcePath); err != nil {
		t.Fatalf("update dir timing: %v", err)
	}
	if err := store.SetRuleCooldown(rule.Name, ruleCooldown); err != nil {
		t.Fatalf("SetRuleCooldown() error = %v", err)
	}

	got, err := store.NextEligibleScanAt()
	if err != nil {
		t.Fatalf("NextEligibleScanAt() error = %v", err)
	}
	if !got.Equal(ruleCooldown.Truncate(time.Second)) {
		t.Fatalf("NextEligibleScanAt() = %s, want %s", got, ruleCooldown.Truncate(time.Second))
	}
}
