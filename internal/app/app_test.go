package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListExistingTargetDirs(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, "movies", "港台"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "tv"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	dirs, err := listExistingTargetDirs(baseDir)
	if err != nil {
		t.Fatalf("listExistingTargetDirs() error = %v", err)
	}

	got := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		got[dir] = struct{}{}
	}
	for _, want := range []string{"movies", "movies/港台", "tv"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("missing dir %q in %v", want, dirs)
		}
	}
}
