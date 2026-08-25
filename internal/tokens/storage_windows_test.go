//go:build windows

package tokens

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsSaveRestrictsCreatedObjectsToTheCurrentUser(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".secrets")
	path := filepath.Join(dir, "tokens.json")
	if err := Save(path, File{Items: []Item{{ItemID: "item-1", AccessToken: "secret"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, objectPath := range []string{dir, path, path + ".lock"} {
		t.Run(filepath.Base(objectPath), func(t *testing.T) {
			assertCurrentUserOnlyDACL(t, objectPath)
		})
	}
}

func TestWindowsTempFileIsRestrictedBeforeSecretBytesAreWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	wantErr := errors.New("stop after checking the temp DACL")
	originalWrite := writeTokenTemp
	writeTokenTemp = func(file *os.File, data []byte) error {
		assertCurrentUserOnlyDACL(t, file.Name())
		return wantErr
	}
	t.Cleanup(func() { writeTokenTemp = originalWrite })

	err := Save(path, File{Items: []Item{{ItemID: "item-1", AccessToken: "secret"}}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Save error = %v, want injected stop", err)
	}
}

func TestWindowsSaveReplacesAnExistingFileWithoutADirectorySyncError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := Save(path, File{Items: []Item{{ItemID: "old", AccessToken: "old-secret"}}}); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	if err := Save(path, File{Items: []Item{{ItemID: "new", AccessToken: "new-secret"}}}); err != nil {
		t.Fatalf("replacement Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, found := got.Find("new"); !found {
		t.Errorf("replacement content = %+v, want new item", got.Items)
	}
}

func assertCurrentUserOnlyDACL(t *testing.T, path string) {
	t.Helper()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatalf("open process token: %v", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("get process user: %v", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("get DACL for %s: %v", path, err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("read DACL for %s: %v", path, err)
	}
	if dacl == nil {
		t.Fatalf("DACL for %s is nil", path)
	}
	if dacl.AceCount != 1 {
		t.Fatalf("DACL for %s has %d entries, want 1", path, dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("get DACL entry for %s: %v", path, err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("DACL entry for %s has type %d, want allow", path, ace.Header.AceType)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.Equals(user.User.Sid) {
		t.Fatalf("DACL for %s grants %s, want only current user %s", path, sid.String(), user.User.Sid.String())
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("read DACL control for %s: %v", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL for %s inherits permissions", path)
	}
}
