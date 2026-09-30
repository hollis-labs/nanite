package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A real turn through the dispatcher records the profile it ran under: name,
// digest and the resolved values with the layer that supplied each. A second
// session on another profile is distinguishable, which is what A/B attribution
// needs.
func TestGenerateResponse_RecordsHarnessProfilePerTurn(t *testing.T) {
	ctx := context.Background()
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("ok")}})
	if err := f.st.UpdateSessionMetadata(ctx, f.session, `{"harness_profile":"conservative","harness_overrides":{"harness":{"hard_ceiling":77}}}`); err != nil {
		t.Fatal(err)
	}
	f.run(t, "recorded-turn")

	rows, err := f.st.GetSessionExecutionMetrics(ctx, f.session)
	if err != nil || len(rows) != 1 {
		t.Fatalf("metrics: %v %d rows", err, len(rows))
	}
	m := rows[0]
	if m.ProfileName != "conservative" || !strings.HasPrefix(m.ProfileDigest, "sha256:") {
		t.Fatalf("recorded profile = %q %q", m.ProfileName, m.ProfileDigest)
	}
	var eff struct {
		Values  map[string]any `json:"values"`
		Sources map[string]struct {
			Layer string `json:"layer"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(m.EffectiveLimitsJSON), &eff); err != nil {
		t.Fatalf("effective json: %v\n%s", err, m.EffectiveLimitsJSON)
	}
	if eff.Values["hard_ceiling"].(float64) != 77 || eff.Sources["hard_ceiling"].Layer != "launch" {
		t.Errorf("hard_ceiling = %v from %q", eff.Values["hard_ceiling"], eff.Sources["hard_ceiling"].Layer)
	}
	if eff.Values["runaway_fail_cap"].(float64) != 5 || eff.Sources["runaway_fail_cap"].Layer != "profile:conservative" {
		t.Errorf("runaway_fail_cap = %v from %q", eff.Values["runaway_fail_cap"], eff.Sources["runaway_fail_cap"].Layer)
	}
	if eff.Sources["compact_preview_bytes"].Layer != "computed" {
		t.Errorf("compact source = %q", eff.Sources["compact_preview_bytes"].Layer)
	}
}

func TestGenerateResponse_DefaultProfileIsRecordedToo(t *testing.T) {
	f := newCharacterizationFixture(t, []characterizationProviderStep{{events: doneEvents("ok")}})
	f.run(t, "default-turn")
	rows, err := f.st.GetSessionExecutionMetrics(context.Background(), f.session)
	if err != nil || len(rows) != 1 || rows[0].ProfileName != "default" || rows[0].EffectiveLimitsJSON == "" {
		t.Fatalf("default recording: %v %+v", err, rows)
	}
}
