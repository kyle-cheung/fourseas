//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tokens

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixSaveUsesOwnerOnlyFileModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".secrets")
	path := filepath.Join(dir, "tokens.json")
	if err := Save(path, File{Items: []Item{{ItemID: "item-1", AccessToken: "secret"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, objectPath := range []string{path, path + ".lock"} {
		info, err := os.Stat(objectPath)
		if err != nil {
			t.Fatalf("stat %s: %v", objectPath, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", objectPath, got)
		}
	}
	directory, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat token directory: %v", err)
	}
	if got := directory.Mode().Perm(); got != 0o700 {
		t.Errorf("token directory mode = %o, want 700", got)
	}
}
