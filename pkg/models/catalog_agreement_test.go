package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSeededValuesAgreeWithCatalog compares every seeded model's context
// window and max output against the models.dev catalog.
//
// # Why this exists rather than a presence check
//
// The first version of this verification only asked whether each seeded model
// EXISTS in the catalog. Presence is not value: six entries were added with
// identifiers derived correctly from the catalog, and the surrounding numbers
// still had to be typed into near-identical structs — which is where
// copy-the-row-above happens. A presence check passes through that completely.
//
// Both directions bite at runtime and neither shows up in a table, which is
// what makes this worth automating rather than eyeballing:
//
//   - MaxOutput ABOVE the real ceiling lets a request ask for more than the
//     model allows, and it fails at the provider.
//   - ContextWindow ABOVE the real limit makes the broker pack more than fits,
//     and the call dies at request time.
//
// A too-low value is merely wasteful, so this asserts equality rather than a
// bound: the registry is supposed to mirror the catalog, and drift in either
// direction means one of them moved.
//
// # This is a bridge, and here is the condition that deletes it
//
// Two sources of truth describe the same numbers: pkg/models' hardcoded slice
// and the models.dev catalog. Per what-a-check-may-assert.md, a check whose job
// is noticing two sources diverging is a bridge that must name its end
// condition rather than a date.
//
//	DELETE THIS TEST WHEN the registry stops carrying context/output values —
//	i.e. when models.dev becomes the floor rather than an enrichment layer over
//	a hardcoded list (CW-20260912-0108). At that point there is one source and
//	nothing to compare.
//
// # Why it skips rather than fails without the cache
//
// The catalog is a runtime disk cache, not a checked-in fixture, so it is
// absent on a machine that has never run the app and in a clean CI container.
// Skipping is correct there — but a skip must not be spellable the same way as
// a pass, so this says what it did not examine.
func TestSeededValuesAgreeWithCatalog(t *testing.T) {
	catalog := loadCatalogForTest(t)
	if catalog == nil {
		return // loadCatalogForTest already called t.Skip with a reason.
	}

	checked, skipped := 0, 0
	for _, m := range AllSeeded() {
		prov, ok := catalog[m.Provider]
		if !ok {
			skipped++
			continue
		}
		entry, ok := prov[m.ModelID]
		if !ok {
			// Expected for the pty-* CLI pseudo-models, which are not API
			// models, and for identifiers models.dev has aged out. Reported,
			// not failed — the catalog not knowing a model is not evidence the
			// registry is wrong about it.
			skipped++
			continue
		}
		checked++

		if entry.Limit.Context != 0 && m.ContextWindow != entry.Limit.Context {
			t.Errorf("%s/%s ContextWindow = %d, catalog says %d — a value above the real "+
				"limit makes the broker pack more than fits and the call dies at request time",
				m.Provider, m.ModelID, m.ContextWindow, entry.Limit.Context)
		}
		if entry.Limit.Output != 0 && m.MaxOutput != entry.Limit.Output {
			t.Errorf("%s/%s MaxOutput = %d, catalog says %d — a value above the real ceiling "+
				"lets a request ask for more than the model allows",
				m.Provider, m.ModelID, m.MaxOutput, entry.Limit.Output)
		}
	}

	if checked == 0 {
		t.Fatalf("compared 0 models against the catalog (%d skipped) — the comparison ran "+
			"but examined nothing, which is not the same as agreeing", skipped)
	}
	t.Logf("compared %d seeded models against the catalog; %d skipped as unknown to it",
		checked, skipped)
}

type catalogLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type catalogModel struct {
	Limit catalogLimit `json:"limit"`
}

// loadCatalogForTest returns provider -> modelID -> entry, or nil after
// calling t.Skip when the on-disk cache is not present.
func loadCatalogForTest(t *testing.T) map[string]map[string]catalogModel {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir (%v); compared 0 models against the catalog", err)
		return nil
	}
	path := filepath.Join(base, "go-modelsdev", "catalog.json")
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed path under the user cache dir.
	if err != nil {
		t.Skipf("models.dev cache absent at %s (%v); compared 0 models against the catalog. "+
			"Run the app once to populate it.", path, err)
		return nil
	}
	var doc struct {
		Data struct {
			Providers map[string]struct {
				Models map[string]catalogModel `json:"models"`
			} `json:"Providers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("models.dev cache at %s is present but unparseable: %v", path, err)
	}
	out := make(map[string]map[string]catalogModel, len(doc.Data.Providers))
	for pid, p := range doc.Data.Providers {
		out[pid] = p.Models
	}
	if len(out) == 0 {
		t.Fatalf("models.dev cache at %s parsed to zero providers — the shape changed and "+
			"this comparison would pass vacuously", path)
	}
	return out
}
