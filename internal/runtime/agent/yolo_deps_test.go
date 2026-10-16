package agent

import (
	"testing"

	"github.com/hollis-labs/libs/harness/core/adapters"
	permissionlib "github.com/hollis-labs/libs/security/policy/permission"
)

// withYolo puts d's permission engine in yolo mode when yolo is true. The
// permission mode is the only thing that selects Claude's developer launch
// variant.
func withYolo(d *Dependencies, yolo bool) *Dependencies {
	if yolo {
		d.Permissions = permissionlib.NewEngine(permissionlib.ModeYolo, nil)
	}
	return d
}

// The developer variant follows the engine's mode at launch time: a default
// engine never gets it, and an engine switched to yolo at runtime does.
func TestSelectAdapter_DeveloperVariantFollowsEffectiveMode(t *testing.T) {
	sel, err := selectRuntime("claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	flagged := func(d *Dependencies) bool {
		a, err := selectAdapter(d, sel, "", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("hi", "", "") {
			if arg == "--dangerously-skip-permissions" {
				return true
			}
		}
		return false
	}
	engine := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	deps := &Dependencies{Permissions: engine}
	if flagged(&Dependencies{}) || flagged(deps) {
		t.Fatal("developer variant used outside yolo mode")
	}
	engine.SetMode(permissionlib.ModeYolo)
	if !flagged(deps) {
		t.Fatal("developer variant not used in yolo mode")
	}
	engine.SetMode(permissionlib.ModePlan)
	if flagged(deps) {
		t.Fatal("developer variant used after leaving yolo mode")
	}
}
