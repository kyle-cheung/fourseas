package tokens

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpsertReplacesInPlaceAndLeavesTheOriginalAlone(t *testing.T) {
	original := File{Items: []Item{
		{ItemID: "item-scotia", AccessToken: "old-scotia"},
		{ItemID: "item-amex", AccessToken: "amex"},
	}}

	updated := original.Upsert(Item{ItemID: "item-scotia", AccessToken: "new-scotia"})

	if len(updated.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(updated.Items))
	}
	if updated.Items[0].AccessToken != "new-scotia" {
		t.Errorf("token = %q, want the new one", updated.Items[0].AccessToken)
	}
	if original.Items[0].AccessToken != "old-scotia" {
		t.Error("Upsert changed the original file")
	}
}

func TestUpsertAddsANewItem(t *testing.T) {
	updated := File{}.Upsert(Item{ItemID: "item-amex"})
	if len(updated.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(updated.Items))
	}
}

func TestDeleteRemovesOneItemAndLeavesTheOriginalAlone(t *testing.T) {
	original := File{Items: []Item{
		{ItemID: "item-scotia", AccessToken: "scotia"},
		{ItemID: "item-amex", AccessToken: "amex"},
	}}

	updated := original.Delete("item-scotia")

	if len(updated.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(updated.Items))
	}
	if updated.Items[0].ItemID != "item-amex" {
		t.Errorf("kept %q, want item-amex", updated.Items[0].ItemID)
	}
	if len(original.Items) != 2 {
		t.Error("Delete changed the original file")
	}
	if unchanged := original.Delete("item-nothing"); len(unchanged.Items) != 2 {
		t.Errorf("unknown delete kept %d items, want 2", len(unchanged.Items))
	}
}

func TestUsableIn(t *testing.T) {
	tests := []struct {
		name string
		item Item
		env  string
		want bool
	}{
		{"same environment", Item{Env: "production"}, "production", true},
		{"different environment", Item{Env: "sandbox"}, "production", false},
		{"no recorded environment is assumed usable", Item{}, "production", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.item.UsableIn(tc.env); got != tc.want {
				t.Errorf("UsableIn(%q) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func TestLoadMissingFileIsEmptyNotAnError(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "nothing.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Items) != 0 {
		t.Errorf("got %d items, want 0", len(f.Items))
	}
}

func TestSaveThenLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".secrets", "tokens.json")
	want := File{Items: []Item{{
		ItemID:      "item-1",
		AccessToken: "test-access-token",
		Institution: "Amex",
		Liabilities: true,
	}}}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Items))
	}
	if got.Items[0].AccessToken != want.Items[0].AccessToken {
		t.Error("access token did not survive save and load")
	}
	if !got.Items[0].Liabilities {
		t.Error("liabilities = false after save and load, want true")
	}
}

func TestSaveDoesNotReplaceTheFileWhenTheTempWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	original := File{Items: []Item{{ItemID: "item-original", AccessToken: "original-token"}}}
	if err := Save(path, original); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	wantErr := errors.New("injected interrupted write")
	originalWrite := writeTokenTemp
	writeTokenTemp = func(file *os.File, data []byte) error {
		if _, err := file.Write(data[:len(data)/2]); err != nil {
			return err
		}
		return wantErr
	}
	t.Cleanup(func() { writeTokenTemp = originalWrite })

	err := Save(path, File{Items: []Item{{ItemID: "item-new", AccessToken: "new-token"}}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Save error = %v, want injected write failure", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ItemID != "item-original" {
		t.Errorf("tokens after failed Save = %+v, want the original file", got.Items)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read token directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("failed Save left temporary file %q", entry.Name())
		}
	}
}

func TestMutateSerializesProcessesAndPreservesBothItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := Save(path, File{}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	dir := filepath.Dir(path)
	firstEntered := filepath.Join(dir, "first-entered")
	secondEntered := filepath.Join(dir, "second-entered")
	releaseFirst := filepath.Join(dir, "release-first")
	first := tokenHelperCommand(path, "item-a", firstEntered, releaseFirst)
	if err := first.Start(); err != nil {
		t.Fatalf("start first token process: %v", err)
	}
	t.Cleanup(func() { _ = first.Process.Kill() })
	waitForPath(t, firstEntered)

	second := tokenHelperCommand(path, "item-b", secondEntered, "")
	if err := second.Start(); err != nil {
		t.Fatalf("start second token process: %v", err)
	}
	t.Cleanup(func() { _ = second.Process.Kill() })
	secondDone := make(chan error, 1)
	go func() { secondDone <- second.Wait() }()
	select {
	case err := <-secondDone:
		t.Fatalf("second process ended before the first released the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
		if _, err := os.Stat(secondEntered); !os.IsNotExist(err) {
			t.Fatalf("second process entered the mutation while the first held the lock: %v", err)
		}
	}

	if err := os.WriteFile(releaseFirst, nil, 0o600); err != nil {
		t.Fatalf("release first process: %v", err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Wait() }()
	waitForProcess(t, "first", firstDone)
	waitForProcess(t, "second", secondDone)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d Items, want both stale writers to be preserved", len(got.Items))
	}
	for _, itemID := range []string{"item-a", "item-b"} {
		if _, found := got.Find(itemID); !found {
			t.Errorf("final file does not contain %s", itemID)
		}
	}
}

func TestMutateSubprocessHelper(t *testing.T) {
	if os.Getenv("FOURSEAS_TOKEN_HELPER") != "1" {
		return
	}
	path := os.Getenv("FOURSEAS_TOKEN_PATH")
	itemID := os.Getenv("FOURSEAS_TOKEN_ITEM")
	entered := os.Getenv("FOURSEAS_TOKEN_ENTERED")
	release := os.Getenv("FOURSEAS_TOKEN_RELEASE")
	_, err := Mutate(path, func(current File) (File, error) {
		if err := os.WriteFile(entered, nil, 0o600); err != nil {
			return File{}, err
		}
		if release != "" {
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(release); err == nil {
					break
				} else if !os.IsNotExist(err) {
					return File{}, err
				}
				if time.Now().After(deadline) {
					return File{}, errors.New("timed out waiting for subprocess release")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		return current.Upsert(Item{ItemID: itemID, AccessToken: itemID + "-token"}), nil
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
}

func tokenHelperCommand(path, itemID, entered, release string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestMutateSubprocessHelper$")
	cmd.Env = append(os.Environ(),
		"FOURSEAS_TOKEN_HELPER=1",
		"FOURSEAS_TOKEN_PATH="+path,
		"FOURSEAS_TOKEN_ITEM="+itemID,
		"FOURSEAS_TOKEN_ENTERED="+entered,
		"FOURSEAS_TOKEN_RELEASE="+release,
	)
	return cmd
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat coordination path: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProcess(t *testing.T, name string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s token process: %v", name, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s token process", name)
	}
}

func TestLoadMissingLiabilitiesDefaultsFalse(t *testing.T) {
	raw := []byte(`{"items":[
		{"item_id":"item-old","access_token":"test-old-token"},
		{"item_id":"item-new","access_token":"test-new-token","liabilities":true}
	]}`)

	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load token file: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(got.Items))
	}
	if got.Items[0].Liabilities {
		t.Error("old item liabilities = true, want false")
	}
	if !got.Items[1].Liabilities {
		t.Error("new item liabilities = false, want true")
	}
}
