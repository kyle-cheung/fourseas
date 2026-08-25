//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tokens

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func ensureTokenDirectory(path string) error {
	return os.MkdirAll(path, 0o700)
}

func openTokenLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%s is a symbolic link", path)
		}
		if errors.Is(err, unix.EISDIR) {
			return nil, fmt.Errorf("%s is not a regular file", path)
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		return nil, errors.Join(errors.New("wrap token lock file descriptor"), unix.Close(fd))
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, errors.Join(fmt.Errorf("%s is not a regular file", path), file.Close())
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func readProtectedTokenFile(path string) (data []byte, err error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%s is a symbolic link", path)
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		return nil, errors.Join(errors.New("wrap token file descriptor"), unix.Close(fd))
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close token file: %w", closeErr))
		}
	}()

	tokenFileOpened()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("inspect token file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return nil, fmt.Errorf("protect token file: %w", err)
	}
	data, err = io.ReadAll(file)
	return data, err
}

func writeAtomic(path string, f File) (err error) {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("create token temp file: %w", err)
	}
	tempPath := temp.Name()
	open := true
	renamed := false
	defer func() {
		if open {
			if closeErr := temp.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close token temp file: %w", closeErr))
			}
		}
		if !renamed {
			if removeErr := os.Remove(tempPath); removeErr != nil && !os.IsNotExist(removeErr) {
				err = errors.Join(err, fmt.Errorf("remove token temp file: %w", removeErr))
			}
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect token temp file: %w", err)
	}
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
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	renamed = true

	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open token directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync token directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close token directory: %w", err)
	}
	return nil
}
