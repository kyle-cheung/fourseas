package tokens

import (
	"path/filepath"
	"testing"
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
	want := File{Items: []Item{{ItemID: "item-1", AccessToken: "secret", Institution: "Amex"}}}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].AccessToken != "secret" {
		t.Errorf("got %+v, want %+v", got.Items, want.Items)
	}
}
