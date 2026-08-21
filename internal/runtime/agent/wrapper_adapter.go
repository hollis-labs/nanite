package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-providers/provider"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

// nativeAdapter implements both adapters.Adapter and adapters.RuntimeAdapter
// (go-agent-wrapper's provider-integration seam) by wrapping the exact
// provider.CLIAdapter Nanite's own composition root already resolved via
// Dependencies.ProviderAdapter — NOT the shipped go-agent-wrapper/adapters/
// {claude,codex,opencode} packages' own CLIAdapter() constructors.
//
// Why a Nanite-side adapter instead of the shipped ones, per this task's own
// step 1 ("...or confirm the shipped packages are usable as-is"): verified
// directly against go-providers that they are NOT usable as-is for two of
// the three providers.
//
//   - Claude: the shipped adapters/claude package always returns
//     provider.NewClaudeAdapterStreamingStdio() — losing cmd/nanite's dev-mode
//     provider.NewClaudeAdapterDevStreamingStdio() (SkipPermissions=true)
//     variant, which Dependencies.ProviderAdapter already resolves correctly
//     per-process. Wrapping the resolved adapter instead of reconstructing a
//     fresh one preserves this.
//   - Codex/OpenCode: the shipped adapters/codex and adapters/opencode
//     packages declare Descriptor{Protocol: ProtocolCodexAppServer /
//     ProtocolOpenCodeNative, Transport: TransportStdio / TransportHTTPSSE},
//     which wrapper.runtimeCaps maps to Capabilities{JsonRpcStdio: true} /
//     Capabilities{ServeHTTP: true} — a long-lived app-server / HTTP+SSE
//     runtime shape. cmd/nanite registers provider.NewCodexAdapter() /
//     provider.NewOpencodeAdapter() (Mode=""), and factory.go's
//     runtimeConfigForAdapter sets no lifecycle Caps flag for either
//     provider today (shouldUseStreamingStdio only matches claude,
//     shouldUsePTY always returns false) — i.e. Nanite runs Codex/OpenCode
//     as agentkit's subprocess-per-turn "adapter runtime" today, a
//     materially different runtime shape than the shipped adapters select.
//     Adopting the shipped packages as-is would silently change Codex/
//     OpenCode's spawn shape — out of this task's scope (factory.go's
//     decision sites are explicitly not touched; Mode/lifecycle policy
//     stays product-owned).
//
// newNativeAdapter derives Descriptor.Protocol/Transport from
// runtimeConfigForAdapter's own Caps output (the untouched, single source of
// truth for the PTY/StreamingStdio decision) rather than re-deriving the
// same decision a second time, so there is zero drift risk between the two.
type nativeAdapter struct {
	providerName string
	cli          provider.CLIAdapter
	protocol     adapters.Protocol
	transport    adapters.Transport
}

// newNativeAdapter builds the wrapper-facing Adapter for one Boot call.
// caps is runtimeConfigForAdapter's own output — the same value agent.go
// already computes today, reused here (not recomputed) as the single
// source of truth for the PTY/StreamingStdio runtime-shape decision.
func newNativeAdapter(providerName string, cli provider.CLIAdapter, caps agentsessions.Capabilities) *nativeAdapter {
	protocol, transport := protocolTransportFromCaps(caps)
	return &nativeAdapter{
		providerName: providerName,
		cli:          cli,
		protocol:     protocol,
		transport:    transport,
	}
}

// protocolTransportFromCaps maps runtimeConfigForAdapter's Capabilities
// output onto the adapters.Descriptor Protocol/Transport pair
// wrapper.Wrapper.Run's runtimeCaps dispatch table expects. Only the two
// flags runtimeConfigForAdapter ever sets are handled:
//
//   - Caps.PTY        -> ProtocolPTYRaw / TransportPTY (dead today —
//     shouldUsePTY always returns false; kept for parity/future use).
//   - Caps.StreamingStdio -> ProtocolClaudeStreamJSON / TransportStdio
//     (claude, every Mode).
//   - neither -> "" / "" (Codex/OpenCode's subprocess-per-turn "adapter
//     runtime" shape) -- wrapper.runtimeCaps's documented fallback case,
//     matching agentkit's from_adapter.go runtime exactly.
func protocolTransportFromCaps(caps agentsessions.Capabilities) (adapters.Protocol, adapters.Transport) {
	switch {
	case caps.PTY:
		return adapters.ProtocolPTYRaw, adapters.TransportPTY
	case caps.StreamingStdio:
		return adapters.ProtocolClaudeStreamJSON, adapters.TransportStdio
	default:
		return "", ""
	}
}

// Name implements adapters.Adapter.
func (a *nativeAdapter) Name() string { return a.providerName }

// Describe implements adapters.Adapter. Interrupt is reported as
// adapters.InterruptProcess uniformly: none of Nanite's current runtime
// shapes (subprocess-per-turn, streaming-stdio) call a native wire-level
// interrupt on Stop today — see manager.go's Stop doc comment and
// internal/service/agent_deps.go:772-776's carried-forward TODO.
func (a *nativeAdapter) Describe() adapters.Descriptor {
	channel := runtimeevents.ChannelStdio
	if a.protocol == adapters.ProtocolClaudeStreamJSON {
		channel = runtimeevents.ChannelClaudeStreamJSON
	}
	return adapters.Descriptor{
		Provider:  a.providerName,
		Protocol:  a.protocol,
		Transport: a.transport,
		Interrupt: adapters.InterruptProcess,
		Channels:  []runtimeevents.SourceChannel{channel},
	}
}

// Resolve implements adapters.Adapter. wrapper.Wrapper.Run calls this only
// for symmetry/validation (rejecting a PTY request the adapter can't
// honor) — the actual spawn argv comes from CLIAdapter().BuildArgs, invoked
// by agentkit/agentsessions directly at Start time, same as pre-migration.
func (a *nativeAdapter) Resolve(rc adapters.ResolveContext) (adapters.Spec, error) {
	binary, _ := a.cli.Detect()
	if binary == "" {
		binary = a.providerName
	}
	return adapters.Spec{
		Binary: binary,
		Args:   a.cli.BuildArgs("", "", ""),
		Env:    rc.Env,
		Cwd:    rc.Cwd,
	}, nil
}

// CLIAdapter implements adapters.RuntimeAdapter by returning the exact
// provider.CLIAdapter Dependencies.ProviderAdapter resolved for this Boot
// call — see the type doc comment for why this must not be a freshly
// constructed shipped-package adapter.
func (a *nativeAdapter) CLIAdapter() provider.CLIAdapter { return a.cli }

var (
	_ adapters.Adapter        = (*nativeAdapter)(nil)
	_ adapters.RuntimeAdapter = (*nativeAdapter)(nil)
)

// envWrappedCLIAdapter wraps a provider.CLIAdapter, overriding Detect to
// point agentkit's spawn code at a per-session shell script instead of the
// real binary — see wrapEnvForSpawn's doc comment for why this exists.
// Name/BuildArgs/ParseLine pass through to the wrapped adapter unchanged
// via Go interface embedding.
type envWrappedCLIAdapter struct {
	provider.CLIAdapter
	scriptPath string
}

func (a *envWrappedCLIAdapter) Detect() (string, bool) {
	if a.scriptPath == "" {
		return a.CLIAdapter.Detect()
	}
	return a.scriptPath, true
}

var _ provider.CLIAdapter = (*envWrappedCLIAdapter)(nil)

// wrapEnvForSpawn wraps adapter so agentkit spawns a small per-session
// shell script (under bootDir) instead of the real binary directly. The
// script clears the inherited environment and re-execs the real binary
// with exactly env, then passes argv through unchanged.
//
// Why this exists — a genuine, confirmed gap in wrapper.Config (v0.3.0,
// task 05a) that this task's own authorization does not extend to fixing
// in the sibling libs/go-agent-wrapper repo:
//
// wrapper.Wrapper.Run's hardcoded agentsessions.StartOptions{} literal
// (wrapper.go, the same literal task 05a extended for WorkspaceDir/LogPath/
// SessionIDPreset/OnSessionID/AutoFireFirstTurn/FirstTurnPayload) never
// sets Env — wrapper.Config has no Env field at all, and
// adapters.Adapter.Resolve's returned Spec.Env is explicitly discarded
// ("informational... this path", wrapper.go's own Run doc comment) rather
// than forwarded. Confirmed directly against agentkit: when
// StartOptions.Env is empty, every runtime kind falls back to
// cmd.Env = os.Environ() (agentkit/agentsessions/streaming_stdio_session.go
// and from_adapter.go, same fallback both places) — the spawned child
// inherits the Nanite DAEMON's own process environment verbatim instead of
// the per-session composed env this package builds via composeEnv +
// Layout.AmendEnv.
//
// For Claude this is harmless (claudeLayout.AmendEnv is a no-op — Claude's
// planted-file discovery is cwd-based: SpawnWorkdir returns bootDir, and
// claude auto-discovers CLAUDE.md/.mcp.json/settings there). For Codex and
// OpenCode it is a real, severe regression: codexLayout.AmendEnv sets
// CODEX_HOME=<bootDir> and opencodeLayout.AmendEnv sets
// OPENCODE_CONFIG_DIR=<bootDir> — the sole mechanism that redirects those
// CLIs at their planted, per-session config.toml / opencode.json
// (approval_policy, sandbox_mode, writable_roots, .mcp.json, hooks).
// Without it the spawned process would silently fall back to the
// OPERATOR's real, global ~/.codex or ~/.config/opencode config — a
// sandbox-restriction bypass, not merely a missing-feature gap.
//
// Two workarounds were considered and rejected before this one:
//   - os.Setenv on the Nanite daemon process itself: unsafe — Nanite spawns
//     concurrent sessions with different boot dirs from multiple
//     goroutines; a process-wide env mutation races across sessions and
//     can leak one session's CODEX_HOME into another's spawn.
//   - Threading env through provider.CLIAdapter.BuildArgs as a CLI flag:
//     provider.CLIAdapter has no env-contribution method, and Codex/
//     OpenCode's config-dir selection is env-var-only, not a documented
//     per-invocation flag.
//
// This wrapper-script indirection needs no wrapper.Config change and no
// process-wide env mutation: the script is a per-session file under
// bootDir (cleaned up by Session.Stop's existing os.RemoveAll(bootDir)),
// and agentkit's spawn code — which resolves the binary path exclusively
// via CLIAdapter.Detect(), confirmed directly against
// streaming_stdio_session.go:51,204-225 (Prepare's preflight check and the
// real exec.Command call both go through the same Detect() call) — spawns
// the script instead of the real binary. The script's own cmd.Env is still
// whatever agentkit falls back to (os.Environ(), irrelevant — the script
// clears it via `env -i` before re-exec-ing the real binary with exactly
// the intended env), giving byte-identical env semantics to the
// pre-migration direct agentsessions.StartOptions.Env path for all three
// providers uniformly.
//
// Returns adapter unchanged (no wrapping) when adapter.Detect() itself
// fails — preserves the pre-migration "binary not found" failure shape
// instead of writing a script doomed to a shell exec error.
func wrapEnvForSpawn(adapter provider.CLIAdapter, providerName, bootDir string, env map[string]string) (provider.CLIAdapter, error) {
	realBinary, ok := adapter.Detect()
	if !ok || realBinary == "" {
		return adapter, nil
	}
	scriptPath, err := writeEnvWrapperScript(bootDir, providerName, realBinary, env)
	if err != nil {
		return nil, err
	}
	return &envWrappedCLIAdapter{CLIAdapter: adapter, scriptPath: scriptPath}, nil
}

// writeEnvWrapperScript writes the script wrapEnvForSpawn's doc comment
// describes into <bootDir>/.wrapper-exec/<providerName>.sh and returns its
// absolute path. 0o700 (owner-only, executable): the script embeds the
// composed env verbatim, which may include provider credentials.
func writeEnvWrapperScript(bootDir, providerName, realBinary string, env map[string]string) (string, error) {
	if bootDir == "" {
		return "", errors.New("writeEnvWrapperScript: empty bootDir")
	}
	if realBinary == "" {
		return "", errors.New("writeEnvWrapperScript: empty realBinary")
	}
	dir := filepath.Join(bootDir, ".wrapper-exec")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("writeEnvWrapperScript: mkdir %q: %w", dir, err)
	}
	scriptPath := filepath.Join(dir, providerName+".sh")

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Generated by nanite (internal/runtime/agent.writeEnvWrapperScript).\n")
	b.WriteString("# Pins the exact per-session env agent.Boot composed, then execs the\n")
	b.WriteString("# real provider binary. See wrapper_adapter.go's wrapEnvForSpawn doc\n")
	b.WriteString("# comment for why this indirection exists.\n")
	b.WriteString("exec env -i \\\n")
	for _, kv := range envMapToSlice(env) {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		b.WriteString("  " + k + "=" + shellQuote(v) + " \\\n")
	}
	b.WriteString("  " + shellQuote(realBinary) + " \"$@\"\n")

	if err := os.WriteFile(scriptPath, []byte(b.String()), 0o700); err != nil {
		return "", fmt.Errorf("writeEnvWrapperScript: write %q: %w", scriptPath, err)
	}
	return scriptPath, nil
}

// shellQuote wraps s in single quotes for safe embedding in the generated
// POSIX sh script, escaping embedded single quotes via the standard
// close-escape-reopen technique ('\'').
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
