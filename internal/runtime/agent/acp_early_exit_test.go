package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// exitAtOnceScript is an "ACP agent" that dies before answering initialize.
const exitAtOnceScript = "#!/bin/sh\necho 'not an ACP agent' >&2\nexit 1\n"

// An ACP agent that exits during launch fails its Boot, not the host
// (CW-20261001-0129). Before go-agent-wrapper v0.21.1 the ACP session closed
// its events channel while drain could still send on it, so an
// immediately-exiting copilot panicked the process with "send on closed
// channel" about 1 run in 4; Nanite refused every ACP launch until it took
// the fix (CW-20260930-0113). These boots take the production path,
// launch.Select's own adapter with no ACPAdapterFactory seam, over Copilot's
// client and the NDJSON bridge (Pi). A panic in any wrapper goroutine kills
// the test binary, so a pass is the assertion.
func TestBoot_ACPAgentExitingDuringLaunchDoesNotPanic(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"copilot", "pi-acp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(exitAtOnceScript), 0o755); err != nil { //nolint:gosec // an executable test fixture in t.TempDir()
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PIACP_CLI_PATH", filepath.Join(bin, "pi-acp"))

	const runs = 40
	for _, provider := range []string{"copilot", "pi"} {
		t.Run(provider, func(t *testing.T) {
			for i := range runs {
				deps, _ := makeBootDeps(t, provider)
				deps.NativeCLIAdapter = nil
				sess, err := Boot(context.Background(), deps, Options{
					Mode:     ModeLongLived,
					Provider: provider,
					Workdir:  t.TempDir(),
				})
				if err == nil {
					_ = sess.Stop(context.Background())
					t.Fatalf("run %d: Boot(%s) succeeded on an agent that exits at once", i, provider)
				}
				if n := deps.Manager.Len(); n != 0 {
					t.Fatalf("run %d: Boot(%s) failed (%v) but left %d sessions registered", i, provider, err, n)
				}
			}
		})
	}
}
