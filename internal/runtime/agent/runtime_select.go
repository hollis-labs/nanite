package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/hollis-labs/libs/harness/core/adapters"
	"github.com/hollis-labs/libs/harness/core/adapters/launch"
	"github.com/hollis-labs/libs/harness/core/adapters/registry"
	"github.com/hollis-labs/libs/llm/core/contracts/runtimes"
	permissionlib "github.com/hollis-labs/libs/security/policy/permission"
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

var errTerminalLaunchUnsupported = errors.New("agent: interactive terminal launches are unsupported; use Tachyon")

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
	if d.DefaultMode == runtimes.ModePTY {
		return RuntimeSelection{}, errTerminalLaunchUnsupported
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
	if sel.Mode == runtimes.ModePTY {
		return false
	}
	for _, k := range launch.Supported() {
		if k.Runtime == sel.Runtime && k.Mode == sel.Mode {
			return true
		}
	}
	return false
}

// launchExtra carries the per-launch arguments that come from the agent profile
// rather than from the runtime selection.
type launchExtra struct {
	// Model is passed to the CLI when its flag is verified for the runtime
	// (modelArgs).
	Model string
	// AddDirs are directories granted on the command line in addition to the
	// work root. Claude ignores the boot dir's permissions.additionalDirectories
	// until its workspace is trusted, and the ephemeral boot dir never is, so
	// the command line is the grant that holds.
	AddDirs []string
	// HostMCPAllow are the Claude allow rules the host's tool grants produce
	// for its projected mcp_servers (host_mcp.go).
	HostMCPAllow []string
	// HostMCPGated marks a launch whose host projects mcp_servers: the allow
	// rules must be the only permission source (see selectAdapter).
	HostMCPGated bool
}

// selectAdapter builds the wrapper adapter for sel through launch.Select.
// Native: the registry's own go-providers adapter, Claude's developer variant
// when the permission engine is in yolo mode, with workRootArgs at the convention's extra
// slot. ACP: the shipped protocol adapter for the mode. deps.NativeCLIAdapter
// and deps.ACPAdapterFactory are test seams; production leaves both nil.
//
// bootDir is the planted boot dir of a native launch, "" for ACP. A native
// Claude launch gets the strict-MCP flags for its .mcp.json (strictMCPArgs)
// and the allow rule for the planted server (claudeMCPAllowArgs) ahead of
// workRootArgs, so the variadic --add-dir stays last.
func selectAdapter(deps *Dependencies, sel RuntimeSelection, workRoot, bootDir string, extra ...launchExtra) (adapters.Adapter, error) {
	var x launchExtra
	if len(extra) > 0 {
		x = extra[0]
	}
	if sel.Mode == runtimes.ModePTY {
		return nil, errTerminalLaunchUnsupported
	}
	if x.HostMCPGated && (sel.Runtime == runtimes.Claude || sel.Runtime == runtimes.Antigravity) && deps.yoloMode() {
		// The mode can change between boot params and launch; the developer
		// variant below would drop the gate the allow rules feed.
		return nil, errHostMCPNoGate
	}
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
			claudeMCPAllowArgs(sel.Runtime, bootDir, deps.MCPConfig, x.HostMCPAllow...),
			hostMCPSettingSourcesArgs(sel.Runtime, x.HostMCPGated),
			modelArgs(string(sel.Runtime), x.Model),
			agyPermissionArgs(deps, sel.Runtime),
			addDirArgs(string(sel.Runtime), workRoot, x.AddDirs),
		),
	}
	if deps.NativeCLIAdapter != nil {
		if cli := deps.NativeCLIAdapter(sel.Runtime); cli != nil {
			selection.CLIAdapter = cli
		}
	}
	if selection.CLIAdapter == nil {
		if sel.Runtime == runtimes.Claude && deps.yoloMode() {
			selection.DeveloperMode = true
			slog.Warn("AUDIT agent launch: Claude developer variant (--dangerously-skip-permissions) because the permission mode is yolo")
		}
	}
	return launch.Select(selection)
}

// yoloMode reports whether the permission engine is in yolo mode right now. It
// is the only thing that selects Claude's developer launch variant.
func (d *Dependencies) yoloMode() bool {
	return d != nil && d.Permissions != nil && d.Permissions.Mode() == permissionlib.ModeYolo
}

// agyPermissionArgs is agy's counterpart of Claude's developer variant: the
// library defines DeveloperMode only for Claude, so agy's
// --dangerously-skip-permissions rides the extra args instead, under the same
// condition (the permission engine is in yolo mode) and with the same audit line.
//
// Without it agy's headless mode auto-denies any tool call that needs a
// permission it cannot prompt for. Outside yolo the MCP tools are allowed
// per tool by rules in the launch's private settings.json (agy_mcp_allow.go),
// not by this flag; Nanite never writes the operator's own ~/.gemini.
func agyPermissionArgs(deps *Dependencies, runtime runtimes.ID) []string {
	if runtime != runtimes.Antigravity {
		return nil
	}
	if !deps.yoloMode() {
		// The MCP tools it may call carry allow rules in the launch's private
		// settings (bootdir_agy_home.go); any other tool needing a permission
		// stays auto-denied headless.
		return nil
	}
	slog.Warn("AUDIT agent launch: agy --dangerously-skip-permissions because the permission mode is yolo")
	return []string{"--dangerously-skip-permissions"}
}
