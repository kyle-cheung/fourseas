//go:build windows

package tokens

import (
	"os"

	"golang.org/x/sys/windows"
)

func acquireFileLock(file *os.File) (func() error, error) {
	handle := windows.Handle(file.Fd())
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return nil, err
	}
	return func() error { return windows.UnlockFileEx(handle, 0, 1, 0, overlapped) }, nil
}
