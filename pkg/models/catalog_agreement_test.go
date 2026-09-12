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
		// Provider FIRST, then model id. The two levels are load-bearing —
		// see loadCatalogForTest. A lookup by model id alone finds a
		// reseller's copy with different limits.
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
//
// # The two levels are load-bearing. Do not flatten this to modelID -> entry.
//
// A model id is not unique in the catalog: resellers republish the same id
// under their own provider with THEIR OWN limits, so an id names a row only
// once a provider has been chosen. Measured against the cache on 2026-09-12:
//
//	claude-sonnet-5   18 providers, 3 distinct output limits, 64000..1000000
//	gpt-5.6            2 providers, 2 distinct contexts,     372000..1050000
//
// The command, so the numbers above can be rechecked rather than trusted.
// One line on purpose: a wrapped one picks up the comment's leading
// whitespace when it is pasted, and python rejects it.
//
//	jq -r '.data.Providers|to_entries[]|select(.value.models["claude-sonnet-5"])|"\(.key)\t\(.value.models["claude-sonnet-5"].limit.output)"' ~/Library/Caches/go-modelsdev/catalog.json | sort -k2 -n
//
// It prints 18 rows, abacus and venice at 64000 through llmgateway at
// 1000000, with anthropic — the only one Nanite should be compared against —
// in the middle at 128000.
//
// This is not hypothetical. The review of the branch that added these models
// reported two of them as wrong; the reviewer had matched on model id alone
// and taken the first hit, comparing Nanite's anthropic and openai rows
// against a reseller's. Both of the "wrong" values they reported — output
// 64000 and context 372000 — are real numbers from the ranges above. It cost
// a blocked merge.
//
// Flattening this map was tried, to see what it would actually do rather than
// to guess. Over 20 runs: 3 passed, 17 failed, and the failures named a
// DIFFERENT model almost every run — 11 distinct assertions across the 20,
// including the reviewer's exact two (claude-sonnet-5 MaxOutput "catalog says
// 64000", gpt-5.6 ContextWindow "catalog says 372000"). Go randomizes map
// iteration, so whichever provider is written last into the flat map wins,
// and that changes per run.
//
// That failure mode is worse than a steady red. An intermittent failure that
// accuses a different model each time reads as "the catalog is unstable" or
// "this test is flaky" — which gets it retried, quarantined or deleted, not
// investigated. It is also occasionally green, so it cannot be relied on to
// stay broken long enough to be diagnosed.
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
