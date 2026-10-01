package agent

import (
	"errors"
	"fmt"
	"slices"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	"github.com/hollis-labs/go-providers/registry"
	"github.com/hollis-labs/nanite/internal/store"
)

// runtime_select.go is Nanite's one launch decision (CW-20260930-0113,
// Sprint 4 piece 3): a provider name resolves to a go-providers registry
// runtime, Nanite picks the mode, and go-agent-wrapper's launch.Select builds
// the adapter, native or ACP. There is no Nanite-side list of providers or
// adapters any more; a runtime the registry carries is a runtime Nanite can
// route to (Copilot and Pi included).

// RuntimeSelection is the runtime and mode one Boot launches.
type RuntimeSelection struct {
	Runtime runtimes.ID
	Mode    runtimes.Mode
}

// ACP reports whether the selection runs over the Agent Client Protocol,
// which plants no boot dir.
func (s RuntimeSelection) ACP() bool { return s.Mode.ACP() }

// nativeModes is Nanite's product policy for the runtimes it drives
// natively. The mode is always passed explicitly, never left to the
// registry default: Codex's default is jsonrpc-stdio (app-server, D-74),
// and Nanite runs Codex per turn. Claude is streaming stdio in every
// Nanite mode (see shouldUseStreamingStdio).
var nativeModes = map[runtimes.ID]runtimes.Mode{
	runtimes.Claude:   runtimes.ModeStreamingStdio,
	runtimes.Codex:    runtimes.ModeSubprocessPerTurn,
	runtimes.OpenCode: runtimes.ModeSubprocessPerTurn,
}

// errUnknownRuntime is a provider name the go-providers registry does not
// carry.
var errUnknownRuntime = errors.New("agent: no such runtime in the registry")

// resolveRuntime maps a Nanite provider name, including the legacy pty-/sub-
// prefixes and registry aliases (claude-code, agy), to its registry runtime.
func resolveRuntime(providerName string) (registry.Descriptor, bool) {
	return registry.Lookup(normalizeProviderName(providerName))
}

// selectRuntime decides how one Boot launches providerName for profile:
//   - protocol=acp on the profile selects the ACP mode on its transport;
//   - otherwise a runtime Nanite drives natively gets its nativeModes mode;
//   - any other registry runtime gets its registry default (Copilot and Pi:
//     acp-stdio; Antigravity: subprocess-per-turn, which Boot then refuses
//     for want of a boot-dir layout).
func selectRuntime(providerName string, profile *store.AgentProfile) (RuntimeSelection, error) {
	d, ok := resolveRuntime(providerName)
	if !ok {
		return RuntimeSelection{}, fmt.Errorf("%w: %q", errUnknownRuntime, providerName)
	}
	if useACPProtocol(profile) {
		return RuntimeSelection{Runtime: d.ID, Mode: acpMode(d, effectiveACPTransport(profile))}, nil
	}
	if mode, ok := nativeModes[d.ID]; ok {
		return RuntimeSelection{Runtime: d.ID, Mode: mode}, nil
	}
	return RuntimeSelection{Runtime: d.ID, Mode: d.DefaultMode}, nil
}

// acpMode is the ACP mode for transport on d: acp-tcp only where the
// registry says the runtime has it (Copilot's daemon), acp-stdio otherwise.
// The stdio-only bridges (Claude, Codex, Pi) have always ignored a tcp
// transport rather than failing, and still do.
func acpMode(d registry.Descriptor, transport adapters.Transport) runtimes.Mode {
	if transport == adapters.TransportTCP && d.Supports(runtimes.ModeACPTCP) {
		return runtimes.ModeACPTCP
	}
	return runtimes.ModeACPStdio
}

// providerSessionSurvivesBoot reports whether a provider session one Boot
// created can be resumed by another, so that its id is worth persisting and
// presetting. Native Codex's cannot: it keeps a thread's rollout under
// CODEX_HOME, which Nanite points at the per-boot dir, so a later Boot has no
// such thread and `exec resume <id>` fails with "no rollout found for thread
// id". go-providers v0.41.0 made Codex exec report a thread id and resume
// from it (CW-20260930-0113, the latest-libs bump); within one Boot that is
// what carries context from turn to turn, and the libs handle it. Presetting
// it across Boots would fail the first turn after every cold boot, where
// before the bump Codex ignored the id and started a fresh thread.
func providerSessionSurvivesBoot(sel RuntimeSelection) bool {
	return sel.ACP() || sel.Runtime != runtimes.Codex
}

// CanLaunch reports whether Nanite can boot providerName with its default
// selection (no ACP override): the registry carries the runtime,
// go-agent-wrapper has a launch factory for the selected mode, and a native
// mode has a Nanite boot-dir layout. Chat routing and the subagent runner
// ask this instead of consulting a registered-adapter index.
func CanLaunch(providerName string) bool {
	return LaunchError(providerName) == nil
}

// LaunchError says why providerName cannot launch with its default
// selection, or returns nil when it can (CanLaunch).
func LaunchError(providerName string) error {
	sel, err := selectRuntime(providerName, nil)
	if err != nil {
		return err
	}
	if !launchSupported(sel) {
		return fmt.Errorf("agent: go-agent-wrapper has no launch for %s %s", sel.Runtime, sel.Mode)
	}
	if sel.ACP() {
		return nil
	}
	if _, unsupported := bootdirLayoutFor(string(sel.Runtime)).(unsupportedLayout); unsupported {
		return fmt.Errorf("agent: no Nanite boot-dir layout for %s yet", sel.Runtime)
	}
	return nil
}

func launchSupported(sel RuntimeSelection) bool {
	for _, k := range launch.Supported() {
		if k.Runtime == sel.Runtime && k.Mode == sel.Mode {
			return true
		}
	}
	return false
}

// selectAdapter builds the wrapper adapter for sel through launch.Select.
// Native: the registry's own go-providers adapter, Claude's developer variant
// when deps.DeveloperMode is set, with workRootArgs at the convention's extra
// slot. ACP: the shipped protocol adapter for the mode. deps.NativeCLIAdapter
// and deps.ACPAdapterFactory are test seams; production leaves both nil.
//
// bootDir is the planted boot dir of a native launch, "" for ACP. A native
// Claude launch gets the strict-MCP flags for its .mcp.json (strictMCPArgs)
// and the allow rule for the planted server (claudeMCPAllowArgs) ahead of
// workRootArgs, so the variadic --add-dir stays last.
func selectAdapter(deps *Dependencies, sel RuntimeSelection, workRoot, bootDir string) (adapters.Adapter, error) {
	if sel.ACP() {
		if deps.ACPAdapterFactory != nil {
			transport := adapters.TransportStdio
			if sel.Mode == runtimes.ModeACPTCP {
				transport = adapters.TransportTCP
			}
			return deps.ACPAdapterFactory(string(sel.Runtime), transport)
		}
		return launch.Select(launch.Selection{Runtime: string(sel.Runtime), Mode: sel.Mode})
	}
	selection := launch.Selection{
		Runtime: string(sel.Runtime),
		Mode:    sel.Mode,
		ExtraArgs: slices.Concat(
			strictMCPArgs(sel.Runtime, bootDir),
			claudeMCPAllowArgs(sel.Runtime, bootDir, deps.MCPConfig),
			workRootArgs(string(sel.Runtime), workRoot),
		),
	}
	if deps.NativeCLIAdapter != nil {
		if cli := deps.NativeCLIAdapter(sel.Runtime); cli != nil {
			selection.CLIAdapter = cli
		}
	}
	if selection.CLIAdapter == nil {
		selection.DeveloperMode = deps.DeveloperMode && sel.Runtime == runtimes.Claude
	}
	return launch.Select(selection)
}
