package tokens

import (
	"os"
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
