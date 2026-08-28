//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepositorySaveUsesOwnerOnlyPermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	repository := Repository{Path: path}
	value := New()
	value.DefaultProfile = "work"
	value.Profiles["work"] = validProfile()
	if err := repository.Save(value); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("configuration permissions = %o, want 600", got)
	}
}
