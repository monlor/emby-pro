package syncer

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/index"
)

func newAdaptiveTestSyncer() *Syncer {
	return &Syncer{
		cfg: config.Config{
			Sync: config.SyncConfig{
				HotInterval:       30 * time.Minute,
				WarmInterval:      6 * time.Hour,
				ColdInterval:      24 * time.Hour,
				HotJitter:         0,
				WarmJitter:        0,
				ColdJitter:        0,
				UnchangedToWarm:   3,
				UnchangedToCold:   7,
				FailureBackoffMax: 24 * time.Hour,
				RuleCooldown:      6 * time.Hour,
			},
		},
		rng: rand.New(rand.NewSource(1)),
	}
}

func TestNextSuccessScheduleAdaptiveTransitions(t *testing.T) {
	s := newAdaptiveTestSyncer()
	now := time.Unix(1_000, 0)

	next, streak, result := s.nextSuccessSchedule(index.DirRecord{
		LastResult:      dirResultUnchanged,
		UnchangedStreak: 2,
	}, false, now)
	if got, want := next, now.Add(6*time.Hour); !got.Equal(want) {
		t.Fatalf("warm next scan = %s, want %s", got, want)
	}
	if streak != 3 || result != dirResultUnchanged {
		t.Fatalf("warm result = (%d,%s), want (3,%s)", streak, result, dirResultUnchanged)
	}

	next, streak, result = s.nextSuccessSchedule(index.DirRecord{
		LastResult:      dirResultUnchanged,
		UnchangedStreak: 6,
	}, false, now)
	if got, want := next, now.Add(24*time.Hour); !got.Equal(want) {
		t.Fatalf("cold next scan = %s, want %s", got, want)
	}
	if streak != 7 || result != dirResultUnchanged {
		t.Fatalf("cold result = (%d,%s), want (7,%s)", streak, result, dirResultUnchanged)
	}

	next, streak, result = s.nextSuccessSchedule(index.DirRecord{
		LastResult:      dirResultUnchanged,
		UnchangedStreak: 5,
	}, true, now)
	if got, want := next, now.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("changed next scan = %s, want %s", got, want)
	}
	if streak != 0 || result != dirResultChanged {
		t.Fatalf("changed result = (%d,%s), want (0,%s)", streak, result, dirResultChanged)
	}

	next, streak, result = s.nextSuccessSchedule(index.DirRecord{}, false, now)
	if got, want := next, now.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("initial next scan = %s, want %s", got, want)
	}
	if streak != 0 || result != dirResultChanged {
		t.Fatalf("initial result = (%d,%s), want (0,%s)", streak, result, dirResultChanged)
	}
}

func TestNextFailureScheduleAdaptiveBackoffAndCooldown(t *testing.T) {
	s := newAdaptiveTestSyncer()
	now := time.Unix(2_000, 0)

	next, cooldownUntil, result := s.nextFailureSchedule(index.DirRecord{FailCount: 0}, now, errors.New("boom"))
	if got, want := next, now.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("first backoff = %s, want %s", got, want)
	}
	if !cooldownUntil.Equal(next) || result != dirResultFailed {
		t.Fatalf("first failure result = (%s,%s), want (%s,%s)", cooldownUntil, result, next, dirResultFailed)
	}

	next, cooldownUntil, result = s.nextFailureSchedule(index.DirRecord{}, now, errors.New("status 403 forbidden"))
	if got, want := next, now.Add(30*time.Minute); !got.Equal(want) {
		t.Fatalf("403 backoff = %s, want %s", got, want)
	}
	if !cooldownUntil.Equal(next) || result != dirResultFailed {
		t.Fatalf("403 result = (%s,%s), want (%s,%s)", cooldownUntil, result, next, dirResultFailed)
	}

	s.cfg.Sync.FailureBackoffMax = 6 * time.Hour
	next, _, _ = s.nextFailureSchedule(index.DirRecord{FailCount: 3}, now, errors.New("boom"))
	if got, want := next, now.Add(6*time.Hour); !got.Equal(want) {
		t.Fatalf("capped backoff = %s, want %s", got, want)
	}

	next, cooldownUntil, result = s.nextFailureSchedule(index.DirRecord{}, now, errors.New("request returned 429"))
	if got, want := next, now.Add(6*time.Hour); !got.Equal(want) {
		t.Fatalf("rule cooldown next = %s, want %s", got, want)
	}
	if !cooldownUntil.Equal(next) || result != dirResultRuleCooldown {
		t.Fatalf("cooldown result = (%s,%s), want (%s,%s)", cooldownUntil, result, next, dirResultRuleCooldown)
	}
}

func TestHasScheduleChangeTracksOutputDiffs(t *testing.T) {
	baseFiles := []index.FileRecord{
		{
			RuleName:    "media",
			SourcePath:  "/media/demo.mp4",
			ParentPath:  "/media",
			TargetPath:  "/strm/media/demo.strm",
			ContentHash: "same",
		},
	}
	baseDirs := []string{"/media/season-1"}
	baseWrites := []fileWrite{
		{
			sourcePath:  "/media/demo.mp4",
			parentPath:  "/media",
			targetPath:  "/strm/media/demo.strm",
			contentHash: "same",
		},
	}
	baseSeenDirs := map[string]struct{}{
		"/media/season-1": {},
	}

	tests := []struct {
		name          string
		existingFiles []index.FileRecord
		existingDirs  []string
		writes        []fileWrite
		seenDirs      map[string]struct{}
		want          bool
	}{
		{
			name:          "unchanged outputs stay unchanged",
			existingFiles: baseFiles,
			existingDirs:  baseDirs,
			writes:        baseWrites,
			seenDirs:      baseSeenDirs,
			want:          false,
		},
		{
			name:          "content hash change is a change",
			existingFiles: baseFiles,
			existingDirs:  baseDirs,
			writes: []fileWrite{{
				sourcePath:  "/media/demo.mp4",
				parentPath:  "/media",
				targetPath:  "/strm/media/demo.strm",
				contentHash: "different",
			}},
			seenDirs: baseSeenDirs,
			want:     true,
		},
		{
			name:          "target path change is a change",
			existingFiles: baseFiles,
			existingDirs:  baseDirs,
			writes: []fileWrite{{
				sourcePath:  "/media/demo.mp4",
				parentPath:  "/media",
				targetPath:  "/strm/media/renamed.strm",
				contentHash: "same",
			}},
			seenDirs: baseSeenDirs,
			want:     true,
		},
		{
			name:          "new child dir is a change",
			existingFiles: baseFiles,
			existingDirs:  baseDirs,
			writes:        baseWrites,
			seenDirs: map[string]struct{}{
				"/media/season-1": {},
				"/media/season-2": {},
			},
			want: true,
		},
		{
			name:          "removed tracked file is a change",
			existingFiles: baseFiles,
			existingDirs:  baseDirs,
			writes:        nil,
			seenDirs:      baseSeenDirs,
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasScheduleChange(tt.existingFiles, tt.existingDirs, tt.writes, tt.seenDirs); got != tt.want {
				t.Fatalf("hasScheduleChange() = %v, want %v", got, tt.want)
			}
		})
	}
}
