package index

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
)

func TestScheduleFullRescanQueuesAllKnownDirs(t *testing.T) {
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
	if len(dirs) != 2 {
		t.Fatalf("expected root and known child dir to be due, got %d", len(dirs))
	}
}

func TestRequestFullRescanQueuesPendingWhenActive(t *testing.T) {
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
	if _, _, err := store.RequestFullRescan(rule.Name, now); err != nil {
		t.Fatalf("RequestFullRescan() error = %v", err)
	}

	scheduled, pending, err := store.RequestFullRescan(rule.Name, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RequestFullRescan() second error = %v", err)
	}
	if scheduled {
		t.Fatalf("expected no new active rescan while one is running")
	}
	if !pending {
		t.Fatalf("expected request to be queued as pending")
	}

	states, err := store.ListRuleStates()
	if err != nil {
		t.Fatalf("ListRuleStates() error = %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 rule state, got %d", len(states))
	}
	if !states[0].FullRescanActive {
		t.Fatalf("expected active full rescan")
	}
	if !states[0].PendingFullRescan {
		t.Fatalf("expected pending full rescan")
	}
}

func TestRequestFullRescanSchedulesAllKnownDirsImmediately(t *testing.T) {
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
	future := now.Add(24 * time.Hour)
	for _, dir := range []string{"/media/a", "/media/b"} {
		if err := store.UpsertDir(rule.Name, dir, "/media", 1, future); err != nil {
			t.Fatalf("UpsertDir(%s) error = %v", dir, err)
		}
	}
	if _, _, err := store.RequestFullRescan(rule.Name, now); err != nil {
		t.Fatalf("RequestFullRescan() error = %v", err)
	}

	dirs, err := store.DueDirs(10, now)
	if err != nil {
		t.Fatalf("DueDirs() error = %v", err)
	}
	if len(dirs) != 3 {
		t.Fatalf("expected root and child dirs to all be due, got %d", len(dirs))
	}
}

func TestAdvanceFullRescanStartsPendingRunImmediatelyAfterCompletion(t *testing.T) {
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
	if err := store.UpsertDir(rule.Name, "/media/movies", "/media", 1, now); err != nil {
		t.Fatalf("UpsertDir() error = %v", err)
	}
	if _, _, err := store.RequestFullRescan(rule.Name, now); err != nil {
		t.Fatalf("RequestFullRescan() error = %v", err)
	}
	if _, _, err := store.RequestFullRescan(rule.Name, now.Add(time.Hour)); err != nil {
		t.Fatalf("RequestFullRescan() pending error = %v", err)
	}

	firstPassDoneAt := now.Add(10 * time.Minute)
	if err := store.MarkDirScannedSuccess(rule.Name, rule.SourcePath, firstPassDoneAt.Add(time.Hour), firstPassDoneAt, 0, "changed", 0, firstPassDoneAt); err != nil {
		t.Fatalf("MarkDirScannedSuccess(root) error = %v", err)
	}
	if err := store.MarkDirScannedSuccess(rule.Name, "/media/movies", firstPassDoneAt.Add(time.Hour), firstPassDoneAt, 0, "changed", 0, firstPassDoneAt); err != nil {
		t.Fatalf("MarkDirScannedSuccess(child) error = %v", err)
	}

	completed, startedPending, err := store.AdvanceFullRescan(rule.Name, firstPassDoneAt)
	if err != nil {
		t.Fatalf("AdvanceFullRescan() error = %v", err)
	}
	if !completed {
		t.Fatalf("expected previous full rescan to complete")
	}
	if !startedPending {
		t.Fatalf("expected pending full rescan to start immediately")
	}

	states, err := store.ListRuleStates()
	if err != nil {
		t.Fatalf("ListRuleStates() error = %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 rule state, got %d", len(states))
	}
	if !states[0].FullRescanActive {
		t.Fatalf("expected new full rescan to remain active")
	}
	if states[0].PendingFullRescan {
		t.Fatalf("expected pending flag to be cleared after restart")
	}
	if !states[0].LastFullRescanAt.Equal(firstPassDoneAt.Truncate(time.Second)) {
		t.Fatalf("last full rescan at = %s, want %s", states[0].LastFullRescanAt, firstPassDoneAt.Truncate(time.Second))
	}

	dirs, err := store.DueDirs(10, firstPassDoneAt)
	if err != nil {
		t.Fatalf("DueDirs() error = %v", err)
	}
	if len(dirs) == 0 || dirs[0].SourcePath != rule.SourcePath {
		t.Fatalf("expected root to be due for the restarted full rescan")
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
