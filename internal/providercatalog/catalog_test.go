package providercatalog

import "testing"

// TestCatalog_AddAndList — basic insertion preserves order and survives
// readout via List(). The dropdown surfaces entries in registration order;
// reordering would change the FE display so the test pins it.
func TestCatalog_AddAndList(t *testing.T) {
	c := New()
	c.Add(Entry{Name: "anthropic", DisplayName: "Anthropic", RowID: "anthropic-001"})
	c.Add(Entry{Name: "openai", DisplayName: "OpenAI", RowID: "openai-001"})
	c.Add(Entry{Name: "tether", DisplayName: "Tether", RowID: "tether-001"})

	got := c.List()
	if len(got) != 3 {
		t.Fatalf("List len: got %d, want 3", len(got))
	}
	want := []string{"anthropic", "openai", "tether"}
	for i, e := range got {
		if e.Name != want[i] {
			t.Errorf("[%d] Name: got %q, want %q", i, e.Name, want[i])
		}
	}
}

// TestCatalog_AddReplacesByName_PreservesPosition — re-registering an
// existing name (e.g. a hot-reload) must not reshuffle the dropdown.
// The replacement entry overwrites the original at the same index.
func TestCatalog_AddReplacesByName_PreservesPosition(t *testing.T) {
	c := New()
	c.Add(Entry{Name: "anthropic", DisplayName: "Anthropic", RowID: "anthropic-001"})
	c.Add(Entry{Name: "openai", DisplayName: "OpenAI", RowID: "openai-001"})
	c.Add(Entry{Name: "anthropic", DisplayName: "Anthropic (v2)", RowID: "anthropic-001"})

	got := c.List()
	if len(got) != 2 {
		t.Fatalf("List len after re-add: got %d, want 2 (replacement, not duplicate)", len(got))
	}
	if got[0].Name != "anthropic" {
		t.Errorf("[0] Name: got %q, want anthropic (position preserved)", got[0].Name)
	}
	if got[0].DisplayName != "Anthropic (v2)" {
		t.Errorf("[0] DisplayName: got %q, want Anthropic (v2) (replacement applied)", got[0].DisplayName)
	}
	if got[1].Name != "openai" {
		t.Errorf("[1] Name: got %q, want openai", got[1].Name)
	}
}

// TestCatalog_Get — lookup by name returns the entry and an ok flag,
// or zero+false for absent. The handler's merge uses this shape.
func TestCatalog_Get(t *testing.T) {
	c := New()
	c.Add(Entry{Name: "anthropic", DisplayName: "Anthropic", RowID: "anthropic-001"})

	got, ok := c.Get("anthropic")
	if !ok {
		t.Fatal("Get(anthropic) ok = false; want true")
	}
	if got.DisplayName != "Anthropic" {
		t.Errorf("DisplayName: got %q, want Anthropic", got.DisplayName)
	}

	if _, ok := c.Get("nonexistent"); ok {
		t.Error("Get(nonexistent) ok = true; want false")
	}
}

// TestCatalog_NilSafe — operations on a nil catalog must not panic.
// nil-safety is documented; handler tests rely on it for the
// "catalog not wired" degraded path.
func TestCatalog_NilSafe(t *testing.T) {
	var c *Catalog
	c.Add(Entry{Name: "x"}) // must not panic
	if got := c.List(); got != nil {
		t.Errorf("nil.List() = %v, want nil", got)
	}
	if _, ok := c.Get("x"); ok {
		t.Error("nil.Get(x) ok = true; want false")
	}
}

// TestCatalog_AddSkipsEmptyName — defensive: an entry with no Name has
// no key to look up, so dropping it silently keeps the byName map
// invariant (every entry is keyed).
func TestCatalog_AddSkipsEmptyName(t *testing.T) {
	c := New()
	c.Add(Entry{Name: "", DisplayName: "X"})
	if got := c.List(); len(got) != 0 {
		t.Errorf("List after empty-name Add: got %d entries, want 0", len(got))
	}
}
