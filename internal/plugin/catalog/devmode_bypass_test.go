package catalog

import "github.com/hollis-labs/nanite/internal/plugin/devmode"

// devmodeBypassActive returns whether this build compiled with the `devmode`
// tag, which disables catalog signature verification. Used by tests that
// want to skip prod-only invariants under devmode builds.
func devmodeBypassActive() bool { return devmode.HostDevSigningBypass }
