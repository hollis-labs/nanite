package skill

import "testing"

// TestDiscover_AlwaysEmpty is TASKS/phase-1/08's negative verification for
// skills: every file-based discovery tier (project .nanite/skills/, user
// ~/.nanite/skills/, Claude Code ecosystem .claude/skills/, plugin
// plugins/*/skills/*.md) was cut in full, and DiscoverOptions carries no
// field left that could point at any of them. Discover() must always return
// an empty result. A real, file-on-disk end-to-end version of this
// verification (a new .md file dropped into a formerly-discovered directory
// before boot, confirmed absent from the DB afterward) is covered by this
// task's real-backup-DB verification step, not a unit test here — there is
// no longer any wiring in this package for a unit test to exercise.
func TestDiscover_AlwaysEmpty(t *testing.T) {
	defs, err := Discover(DiscoverOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("got %d defs, want 0 (every file-based skill discovery tier is cut)", len(defs))
	}
}
