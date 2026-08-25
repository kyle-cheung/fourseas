//go:build windows

package tokens

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows does not use Unix mode bits for confidentiality. Every directory
// and file that this package creates gets a protected DACL that grants full
// access only to the user who runs fourseas. The DACL is supplied to the
// create call, before a handle is returned and before secret bytes are written.
func currentUserSecurity() (*windows.SecurityAttributes, *windows.ACL, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;FA;;;" + user.User.Sid.String() + ")",
	)
	if err != nil {
		return nil, nil, err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return nil, nil, err
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	return attributes, dacl, nil
}

func ensureTokenDirectory(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return err
	}
	if err := ensureTokenDirectory(parent); err != nil {
		return err
	}
	attributes, _, err := currentUserSecurity()
	if err != nil {
		return fmt.Errorf("build directory DACL: %w", err)
	}
	windowsPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = windows.CreateDirectory(windowsPath, attributes)
	runtime.KeepAlive(attributes)
	if err == nil {
		return nil
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return statErr
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func openTokenLock(path string) (*os.File, error) {
	attributes, _, err := currentUserSecurity()
	if err != nil {
		return nil, fmt.Errorf("build lock DACL: %w", err)
	}
	windowsPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		windowsPath,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		attributes,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	runtime.KeepAlive(attributes)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		windows.CloseHandle(handle)
		return nil, errors.New("wrap token lock handle")
	}
	if err := protectTokenFile(path); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// protectTokenFile upgrades token and lock files created by older versions.
// Newly created objects are already protected at creation time.
func protectTokenFile(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	attributes, dacl, err := currentUserSecurity()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
	runtime.KeepAlive(attributes)
	return err
}

func createProtectedTemp(dir, base string) (*os.File, error) {
	attributes, _, err := currentUserSecurity()
	if err != nil {
		return nil, fmt.Errorf("build temp file DACL: %w", err)
	}
	for range 100 {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "."+base+".tmp-"+hex.EncodeToString(random))
		windowsPath, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		handle, err := windows.CreateFile(
			windowsPath,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			attributes,
			windows.CREATE_NEW,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		runtime.KeepAlive(attributes)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(handle), path)
		if file == nil {
			windows.CloseHandle(handle)
			return nil, errors.New("wrap token temp handle")
		}
		return file, nil
	}
	return nil, errors.New("could not allocate a unique token temp file")
}

func replaceTokenFile(from, to string) error {
	windowsFrom, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	windowsTo, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		windowsFrom,
		windowsTo,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func writeAtomic(path string, f File) (err error) {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	dir := filepath.Dir(path)
	temp, err := createProtectedTemp(dir, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create token temp file: %w", err)
	}
	tempPath := temp.Name()
	open := true
	replaced := false
	defer func() {
		if open {
			if closeErr := temp.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close token temp file: %w", closeErr))
			}
		}
		if !replaced {
			if removeErr := os.Remove(tempPath); removeErr != nil && !os.IsNotExist(removeErr) {
				err = errors.Join(err, fmt.Errorf("remove token temp file: %w", removeErr))
			}
		}
	}()

	if err := writeTokenTemp(temp, data); err != nil {
		return fmt.Errorf("write token temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync token temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		open = false
		return fmt.Errorf("close token temp file: %w", err)
	}
	open = false
	if err := replaceTokenFile(tempPath, path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	replaced = true
	return nil
}
