//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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

func TestUnixLoadProtectsALegacyTokenFileBeforeReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	writeLegacyTokenFile(t, path, 0o644)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, found := got.Find("item-legacy"); !found {
		t.Errorf("loaded items = %+v, want the legacy item", got.Items)
	}
	assertMode(t, path, 0o600)
}

func TestUnixMutateProtectsALegacyTokenFileBeforeItsCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	writeLegacyTokenFile(t, path, 0o644)

	_, err := Mutate(path, func(current File) (File, error) {
		assertMode(t, path, 0o600)
		if _, found := current.Find("item-legacy"); !found {
			t.Errorf("callback items = %+v, want the legacy item", current.Items)
		}
		return current, nil
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
}

func TestUnixLoadMissingTokenFileRemainsSuccessful(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if len(got.Items) != 0 {
		t.Errorf("missing file loaded %d items, want 0", len(got.Items))
	}
}

func TestUnixLoadRejectsASymlinkWithoutTouchingItsTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	writeLegacyTokenFile(t, target, 0o644)
	path := filepath.Join(dir, "tokens.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("create token symlink: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Load symlink error = %v, want a symbolic-link rejection", err)
	}
	assertMode(t, target, 0o644)
}

func TestUnixLoadRejectsANonRegularPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("create directory at token path: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Load directory error = %v, want a non-regular-file rejection", err)
	}
}

func TestUnixLoadRejectsANamedPipeWithoutWaitingForAWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("create named pipe at token path: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Load(path)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("Load named-pipe error = %v, want a non-regular-file rejection", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Load blocked waiting for a named-pipe writer")
	}
}

func TestUnixLoadReadsTheFileDescriptorOpenedBeforeAPathSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	writeTokenFileWithID(t, path, "item-original", 0o644)
	replacement := filepath.Join(dir, "replacement.json")
	writeTokenFileWithID(t, replacement, "item-replacement", 0o644)
	movedOriginal := filepath.Join(dir, "moved-original.json")
	originalHook := tokenFileOpened
	tokenFileOpened = func() {
		if err := os.Rename(path, movedOriginal); err != nil {
			t.Fatalf("move original after open: %v", err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatalf("replace path after open: %v", err)
		}
	}
	t.Cleanup(func() { tokenFileOpened = originalHook })

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, found := got.Find("item-original"); !found {
		t.Fatalf("loaded items = %+v, want the originally opened file", got.Items)
	}
	if _, found := got.Find("item-replacement"); found {
		t.Fatal("Load followed the replaced pathname")
	}
	assertMode(t, movedOriginal, 0o600)
	assertMode(t, path, 0o644)
}

func TestUnixMutateRejectsASymlinkLockWithoutTouchingItsTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	lockTarget := filepath.Join(dir, "lock-target")
	if err := os.WriteFile(lockTarget, nil, 0o644); err != nil {
		t.Fatalf("write lock target: %v", err)
	}
	if err := os.Chmod(lockTarget, 0o644); err != nil {
		t.Fatalf("set lock target mode: %v", err)
	}
	if err := os.Symlink(lockTarget, path+".lock"); err != nil {
		t.Fatalf("create lock symlink: %v", err)
	}

	_, err := Mutate(path, func(current File) (File, error) { return current, nil })
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Mutate lock symlink error = %v, want a symbolic-link rejection", err)
	}
	assertMode(t, lockTarget, 0o644)
}

func TestUnixMutateRejectsANonRegularLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatalf("create directory at lock path: %v", err)
	}

	_, err := Mutate(path, func(current File) (File, error) { return current, nil })
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Mutate directory lock error = %v, want a non-regular-file rejection", err)
	}
}

func TestUnixMutateRejectsANamedPipeLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	if err := unix.Mkfifo(path+".lock", 0o600); err != nil {
		t.Fatalf("create named pipe at lock path: %v", err)
	}

	_, err := Mutate(path, func(current File) (File, error) { return current, nil })
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Mutate named-pipe lock error = %v, want a non-regular-file rejection", err)
	}
}

func TestUnixMutateProtectsAnExistingRegularLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatalf("write legacy lock: %v", err)
	}
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatalf("set legacy lock mode: %v", err)
	}

	_, err := Mutate(path, func(current File) (File, error) {
		assertMode(t, lockPath, 0o600)
		return current, nil
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
}

func writeLegacyTokenFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	writeTokenFileWithID(t, path, "item-legacy", mode)
}

func writeTokenFileWithID(t *testing.T, path, itemID string, mode os.FileMode) {
	t.Helper()
	data, err := json.Marshal(File{Items: []Item{{ItemID: itemID, AccessToken: "legacy-secret"}}})
	if err != nil {
		t.Fatalf("encode legacy token file: %v", err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write legacy token file: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("set legacy token mode: %v", err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}
