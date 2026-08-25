//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tokens

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func ensureTokenDirectory(path string) error {
	return os.MkdirAll(path, 0o700)
}

func openTokenLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func protectTokenFile(string) error {
	return nil
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
