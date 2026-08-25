// Package tokens saves Plaid access tokens to a local file.
//
// The file holds live credentials. Keep it out of git.
package tokens

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Item is one linked institution.
//
// Env records the Plaid environment the token belongs to. A token is only
// usable in the environment that issued it. Items saved before this field
// existed have an empty Env.
type Item struct {
	ItemID      string    `json:"item_id"`
	AccessToken string    `json:"access_token"`
	Institution string    `json:"institution"`
	Env         string    `json:"env"`
	Liabilities bool      `json:"liabilities"`
	LinkedAt    time.Time `json:"linked_at"`
}

// UsableIn reports whether the item belongs to env. An item with no recorded
// environment is assumed to be usable, because the only way to know is to try.
func (i Item) UsableIn(env string) bool {
	return i.Env == "" || i.Env == env
}

// File is the whole token file.
type File struct {
	Items []Item `json:"items"`
}

// Load reads the token file. A file that does not exist is an empty file, not
// an error: the user has simply not linked anything yet.
func Load(path string) (File, error) {
	data, err := readProtectedTokenFile(path)
	if os.IsNotExist(err) {
		return File{}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

// Upsert returns a new File with item added or replaced. It does not change f.
func (f File) Upsert(item Item) File {
	out := File{Items: make([]Item, 0, len(f.Items)+1)}
	replaced := false
	for _, existing := range f.Items {
		if existing.ItemID == item.ItemID {
			out.Items = append(out.Items, item)
			replaced = true
			continue
		}
		out.Items = append(out.Items, existing)
	}
	if !replaced {
		out.Items = append(out.Items, item)
	}
	return out
}

// Delete returns a new File without the item. It does not change f. An item
// that is not there is not an error: the wanted state is already the state.
func (f File) Delete(itemID string) File {
	out := File{Items: make([]Item, 0, len(f.Items))}
	for _, existing := range f.Items {
		if existing.ItemID == itemID {
			continue
		}
		out.Items = append(out.Items, existing)
	}
	return out
}

// Find returns the item with this id.
func (f File) Find(itemID string) (Item, bool) {
	for _, item := range f.Items {
		if item.ItemID == itemID {
			return item, true
		}
	}
	return Item{}, false
}

// Save writes the file with owner-only permissions.
func Save(path string, f File) error {
	_, err := Mutate(path, func(File) (File, error) { return f, nil })
	return err
}

// Mutate reloads and replaces the token file while it holds an exclusive
// cross-process lock. The callback must derive its result from current.
func Mutate(path string, mutate func(current File) (File, error)) (saved File, err error) {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := ensureTokenDirectory(dir); err != nil {
			return File{}, fmt.Errorf("create %s: %w", dir, err)
		}
	}

	lock, err := openTokenLock(path + ".lock")
	if err != nil {
		return File{}, fmt.Errorf("open token lock: %w", err)
	}
	unlock, err := acquireFileLock(lock)
	if err != nil {
		lock.Close()
		return File{}, fmt.Errorf("lock token file: %w", err)
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("unlock token file: %w", unlockErr))
		}
		if closeErr := lock.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close token lock: %w", closeErr))
		}
	}()

	current, err := Load(path)
	if err != nil {
		return File{}, err
	}
	updated, err := mutate(current)
	if err != nil {
		return File{}, err
	}
	if err := writeAtomic(path, updated); err != nil {
		return File{}, err
	}
	return updated, nil
}

var writeTokenTemp = func(file *os.File, data []byte) error {
	_, err := file.Write(data)
	return err
}

var tokenFileOpened = func() {}
