// Package launcher is Nanite's standalone agent-launch path: it starts a
// Nanite-managed CLI agent (Claude / Codex / OpenCode) directly from a
// shared launch profile, with no chat service and no Tether MCP in the
// loop.
//
// # Why this exists
//
// Nanite's primary launch path is the chat runtime — driveBootSession
// resolves a "bootprofile:<id>" dropdown selection, overlays a compiled
// bootprofile.LaunchSpec onto agent.Options, and calls agent.Boot. That
// path is owned by the chat service and is the right entry point for an
// interactive chat session.
//
// CW-20260515-0027 adds an ADDITIONAL path for operators who want to
// start a Nanite-managed agent from a launch profile WITHOUT a running
// chat server, a browser dropdown, or a Tether orchestrator: a plain
// `nanite launch <profile>` CLI invocation. The standalone launcher
// loads a catalog off disk, compiles the profile through the exact same
// bootprofile compiler the chat path uses, resolves any deferred slots,
// projects the result onto a shared agentlaunch.LaunchPlan (the
// CW-0024 bridge) for a real plan-level validation, and then hands the
// compiled LaunchSpec to Nanite's runtime via agent.Boot.
//
// # Pipeline
//
//	bootprofile.LoadCatalog          load the on-disk catalog
//	bootprofile.CompileFromCatalog   compile profile+launch → LaunchSpec
//	bootprofile.ResolveRequirements  drain deferred cmd/http/role/skill slots
//	buildLaunchPlan                  resolve runtime binding registry-primary
//	  → agentlaunch.PlanFromLaunch   assemble + Validate the shared plan (S5)
//	agent.Boot                       Nanite runtime start (shared bootdir planting)
//
// # Relationship to providerplant
//
// The CW-0025 handoff suggested the standalone path could drive
// providerplant.Plant / PrepareAndPlant on the LaunchPlan. We
// deliberately do NOT: providerplant renders go-providers' own
// CLAUDE.md / .mcp.json, which diverges byte-for-byte from Nanite's
// bootdir content (envelope schema, agent-context doc, subprocess-spawn
// MCP descriptor). agent.Boot already plants the bootdir through the
// shared agentlaunch.InjectionSpec path-safe write loop (CW-0025's
// bootdir_plant.go) using Nanite's renderers, so the operator gets
// shared bootdir behavior AND Nanite-correct content. The LaunchPlan is
// still built and Validate-d so the standalone path exercises the
// shared-plan contract (provider×runtime matrix lookup, path
// expansion, sentinel errors) — it just is not the planting mechanism.
// See docs/standalone-launcher.md for the full rationale.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/agentkit/agentlaunch"
	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/nanite/internal/agentregistry"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/permission"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// Config describes a single standalone launch. The zero value is not
// usable — CatalogPath and Profile are required.
type Config struct {
	// CatalogPath is the boot-profile catalog root. The launcher reads
	// <CatalogPath>/boot-profiles/*.yaml and <CatalogPath>/launches/*.yaml.
	CatalogPath string

	// Profile is the boot-profile ID to launch (the `id:` field of a
	// boot-profile YAML). The profile must reference a launch that
	// declares a provider — a prompt-only profile is rejected because it
	// has no runtime target.
	Profile string

	// Vars is the caller-supplied substitution map merged into the
	// compiler's variable set (caller wins over profile inline vars).
	// Optional.
	Vars map[string]string

	// CLIAdapters is the set of provider CLI adapters the runtime may
	// dispatch to (claude / codex / opencode). Required — without a
	// matching adapter for the profile's provider, Boot cannot spawn.
	// cmd/nanite builds these via initProviders.
	CLIAdapters []provider.CLIAdapter

	// WorkspacesRoot is the persistent workspace base dir. Empty makes
	// buildDeps fall back to the canonical ~/.nanite/workspaces — the
	// same default service.BuildAgentDependencies applies for chat
	// sessions. (agent.Boot itself does NOT default; it hard-errors on an
	// empty root, so the launcher resolves it before handing deps over.)
	WorkspacesRoot string

	// BinaryPath is the absolute path to the nanite binary, used to
	// plant the per-session .mcp.json subprocess descriptor. Empty
	// disables MCP planting (still a valid launch).
	BinaryPath string

	// CLIWritableRoots is the directory allow-list the launched CLI
	// agent (codex / claude) may write to beyond its boot dir. Threaded
	// onto Dependencies.CLIWritableRoots so the boot-dir layout widens
	// the planted provider config (codex [sandbox_workspace_write],
	// claude permissions.additionalDirectories). Sourced from the nanite
	// dev_tools_allowed_paths config setting (CW-20260518-0075). Empty
	// leaves the launched agent confined to its boot dir cwd.
	CLIWritableRoots []string

	// DBPath is the store DB the planted MCP subprocess descriptor
	// points at. Empty disables MCP planting.
	DBPath string

	// Store, when non-nil, backs the runtime lifecycle row in the
	// agent_runtime table so an operator can introspect / orphan-sweep
	// the launch like any chat-spawned session. When nil the launcher
	// uses an in-memory runtime store — the launch still works, it is
	// just not persisted. The standalone CLI subcommand passes a real
	// store; tests pass nil.
	Store *store.Store

	// Wait, when true, blocks Launch until the spawned agent process
	// exits. When false Launch returns as soon as the process is
	// started. The CLI subcommand sets this true so `nanite launch`
	// behaves like a foreground command.
	Wait bool

	// Registry is the shared go-agent-launch directory registrar
	// (FileBackedRegistrar + DegradingRegistrar + LastKnownGoodCache).
	// When set, runtime-binding resolution is registry-primary with an
	// explicit, observable fallback to the spec/profile default (D1 +
	// §4.1). When nil — the standalone offline path — resolution is
	// fully file/spec-default and the launch still works (D1: the
	// registry is NEVER mandatory on the launch hot path).
	Registry *agentregistry.Registry
}

// Result is what a successful Launch returns.
type Result struct {
	// SessionID is the runtime session id agent.Boot assigned.
	SessionID string

	// Provider is the bare adapter name the launch resolved to.
	Provider string

	// BootDir is the ephemeral $TMPDIR boot directory the runtime
	// planted for this launch.
	BootDir string

	// WorkspaceDir is the workspace directory reserved for the launch.
	WorkspaceDir string

	// Plan is the shared agentlaunch.LaunchPlan the launcher compiled
	// and validated. Surfaced so a caller (or a dry-run) can inspect the
	// shared-vocabulary projection without re-deriving it.
	Plan agentlaunch.LaunchPlan

	// Spec is the compiled, requirement-resolved Nanite LaunchSpec the
	// runtime booted from.
	Spec *bootprofile.LaunchSpec
}

// Plan is the no-side-effects half of the pipeline: it loads the
// catalog, compiles the named profile, resolves deferred slots, and
// projects + validates a shared agentlaunch.LaunchPlan — WITHOUT
// touching the runtime. It is the dry-run / compile-check entry point
// (`nanite launch --dry-run`) and is also called internally by Launch.
//
// A prompt-only profile (no launch / no provider) is rejected here:
// LaunchPlan.Validate returns agentlaunch.ErrMissingProviderID, which
// Plan wraps with a pointed message.
func Plan(ctx context.Context, cfg Config) (*bootprofile.LaunchSpec, agentlaunch.LaunchPlan, error) {
	if strings.TrimSpace(cfg.CatalogPath) == "" {
		return nil, agentlaunch.LaunchPlan{}, errors.New("launcher: CatalogPath is required")
	}
	if strings.TrimSpace(cfg.Profile) == "" {
		return nil, agentlaunch.LaunchPlan{}, errors.New("launcher: Profile is required")
	}

	// 1. Load the on-disk catalog.
	cat, err := bootprofile.LoadCatalog(cfg.CatalogPath)
	if err != nil {
		return nil, agentlaunch.LaunchPlan{}, fmt.Errorf("launcher: load catalog: %w", err)
	}

	// 2. Compile the profile (+ paired launch) into a LaunchSpec — the
	//    same compiler the chat dropdown path uses.
	spec, err := bootprofile.CompileFromCatalog(cat, cfg.Profile, bootprofile.Vars(cfg.Vars))
	if err != nil {
		return nil, agentlaunch.LaunchPlan{}, fmt.Errorf("launcher: compile profile %q: %w", cfg.Profile, err)
	}

	// 3. Side-effect-free precheck. ResolveRequirementsContext (step 4)
	//    runs cmd/http deferred resolvers — those execute commands and
	//    make network calls. A prompt-only profile (no launch provider)
	//    can never produce a valid LaunchPlan, so reject it HERE, before
	//    any resolver fires, instead of letting plan.Validate catch it
	//    after the side effects already ran. The compiler normalizes a
	//    launchable profile's provider to a non-empty bare name; an empty
	//    Provider is exactly the agentlaunch.ErrMissingProviderID case.
	if strings.TrimSpace(spec.Provider) == "" {
		return nil, agentlaunch.LaunchPlan{}, fmt.Errorf(
			"launcher: profile %q is prompt-only (no launch provider) and cannot be started: %w",
			cfg.Profile, agentlaunch.ErrMissingProviderID)
	}

	// 4. Drain deferred-slot Requirements (cmd / http / role_summary /
	//    skill_index) BEFORE projecting to a LaunchPlan — an unresolved
	//    spec has an empty BootPrompt, so the inline boot profile would
	//    carry an empty boot body (CW-0026 handoff §7).
	if err := bootprofile.ResolveRequirementsContext(ctx, spec); err != nil {
		return nil, agentlaunch.LaunchPlan{}, fmt.Errorf("launcher: resolve requirements: %w", err)
	}

	// 5. Assemble the shared agentlaunch.LaunchPlan via the shipped
	//    agentlaunch.PlanFromLaunch bridge (S5 Phase C — replaces the
	//    retired hand-rolled ToLaunchPlan). buildLaunchPlan resolves the
	//    runtime binding registry-primary with an explicit file/spec
	//    fallback (D1 + §4.1), resolves the agent identity caller-side
	//    (§4.2), and runs PlanFromLaunch — which itself Validate()s the
	//    assembled plan. This is the shared-plan convergence gate.
	plan, err := buildLaunchPlan(spec, cfg.Registry, slog.Default())
	if err != nil {
		if errors.Is(err, agentlaunch.ErrMissingProviderID) {
			return nil, agentlaunch.LaunchPlan{}, fmt.Errorf(
				"launcher: profile %q is prompt-only (no launch provider) and cannot be started: %w",
				cfg.Profile, err)
		}
		return nil, agentlaunch.LaunchPlan{}, fmt.Errorf("launcher: launch plan invalid: %w", err)
	}

	return spec, plan, nil
}

// Launch runs the full standalone pipeline: Plan, then start the agent
// through Nanite's runtime via agent.Boot. When cfg.Wait is true it
// blocks until the spawned process exits.
//
// The returned Result carries the session id, the boot/workspace dirs,
// the validated shared plan, and the compiled spec.
func Launch(ctx context.Context, cfg Config) (*Result, error) {
	// Side-effect-free launch precheck. Plan runs deferred cmd/http
	// resolvers (commands + network calls); validate the cheap launch
	// prerequisites FIRST so a caller missing adapters fails fast without
	// triggering catalog-authored side effects. This pairs with the
	// no-provider precheck inside Plan — both reject before resolution.
	if len(cfg.CLIAdapters) == 0 {
		return nil, errors.New("launcher: CLIAdapters is required to start a launch")
	}

	spec, plan, err := Plan(ctx, cfg)
	if err != nil {
		return nil, err
	}

	deps, err := buildDeps(cfg)
	if err != nil {
		return nil, fmt.Errorf("launcher: build runtime dependencies: %w", err)
	}

	// Project the compiled LaunchSpec onto agent.Options. This reuses
	// the SAME field-by-field overlay the chat path documents in
	// applyLaunchSpecToBootOpts — provider, env, extra args, boot
	// prompt override, workdir — so a standalone launch and a chat
	// boot-profile launch reach agent.Boot with identical semantics.
	opts := bootOptionsFor(spec)

	sess, err := runtimeagent.Boot(ctx, deps, opts)
	if err != nil {
		return nil, fmt.Errorf("launcher: boot agent: %w", err)
	}

	res := &Result{
		SessionID:    sess.ID,
		Provider:     sess.Provider,
		BootDir:      sess.BootDir,
		WorkspaceDir: sess.WorkspaceDir,
		Plan:         plan,
		Spec:         spec,
	}

	if cfg.Wait {
		if err := sess.Wait(ctx); err != nil {
			return res, fmt.Errorf("launcher: wait for session %s: %w", sess.ID, err)
		}
	}
	return res, nil
}

// bootOptionsFor projects a compiled LaunchSpec onto agent.Options for a
// standalone launch. It mirrors service.applyLaunchSpecToBootOpts but
// starts from an empty Options (no chat-layer caller fields), so the
// precedence rules collapse to "spec value wins" for every field.
//
// Mode is ModeLongLived — a standalone launch is an interactive,
// across-turns agent process, the same lifecycle a chat boot-profile
// session gets.
func bootOptionsFor(spec *bootprofile.LaunchSpec) runtimeagent.Options {
	opts := runtimeagent.Options{
		Mode: runtimeagent.ModeLongLived,
	}
	if spec == nil {
		return opts
	}
	opts.Provider = spec.Provider
	opts.Workdir = spec.Workdir
	opts.Role = spec.Identity.Role
	if spec.BootPrompt != "" {
		opts.BootPromptOverride = spec.BootPrompt
	}
	if len(spec.Env) > 0 {
		opts.Env = make(map[string]string, len(spec.Env))
		for k, v := range spec.Env {
			opts.Env[k] = v
		}
	}
	if len(spec.Args) > 0 {
		opts.ExtraArgs = append([]string(nil), spec.Args...)
	}
	// SessionMeta records the launch provenance so an operator (or an
	// orphan sweep) can see this row came from the standalone launcher.
	opts.SessionMeta = map[string]any{
		"launch_source": "standalone-launcher",
		"profile_id":    spec.ProfileID,
		"launch_id":     spec.LaunchID,
	}
	return opts
}

// buildDeps assembles a minimal runtimeagent.Dependencies for a
// standalone launch. It deliberately does NOT route through
// service.BuildAgentDependencies — that constructor requires the chat
// service's *StreamManager and wires the recovery broker, the
// agentEventBridge, and the boot-profile recovery adapter, none of
// which a one-shot standalone launch needs. Keeping the wiring local
// makes the launcher genuinely independent of internal/service.
//
// The runtime row + agent-profile resolution are backed by the real
// store when cfg.Store is set, and by in-memory stubs otherwise — the
// launch works either way.
func buildDeps(cfg Config) (*runtimeagent.Dependencies, error) {
	var (
		runtimeStore runtimeagent.RuntimeStore
		stateSink    agentsessions.StateSink
	)
	if cfg.Store != nil {
		runtimeStore = &storeRuntimeStore{store: cfg.Store}
		stateSink = &storeStateSink{store: cfg.Store}
	} else {
		mem := newMemoryRuntimeStore()
		runtimeStore = mem
		stateSink = mem
	}

	manager := agentsessions.NewManager(stateSink)

	adapterIndex := indexAdapters(cfg.CLIAdapters)
	providerAdapter := func(name string) provider.CLIAdapter {
		if name == "" {
			return nil
		}
		if a, ok := adapterIndex[stripProviderPrefix(name)]; ok {
			return a
		}
		return nil
	}

	// WorkspacesRoot fallback. agent.Boot's workspaceCreate hard-errors on
	// an empty root — it does NOT default. The chat path resolves this in
	// service.BuildAgentDependencies; the standalone launcher must do the
	// same so a plain `nanite launch <profile>` (which does not set
	// Config.WorkspacesRoot) lands its persistent workspace under the
	// canonical ~/.nanite/workspaces, matching a chat-spawned session.
	//
	// Round-1 review: use permission.HomeDir() (not os.UserHomeDir()) so
	// the fallback still resolves in a HOME-less launchd-style env — bare
	// os.UserHomeDir() would fail there, leave workspacesRoot empty, and
	// the launch would die later with a confusing "WorkspacesRoot is
	// required". A genuine double-probe failure returns an explicit error
	// here instead.
	workspacesRoot := cfg.WorkspacesRoot
	if workspacesRoot == "" {
		home, err := permission.HomeDir()
		if err != nil {
			return nil, fmt.Errorf("launcher: resolve default workspaces root: %w", err)
		}
		workspacesRoot = filepath.Join(home, "."+brand.ID, "workspaces")
	}

	deps := &runtimeagent.Dependencies{
		Agents:          &defaultProfiles{store: cfg.Store},
		SessionsManager: manager,
		Store:           runtimeStore,
		ProviderAdapter: providerAdapter,
		MCPConfig: runtimeagent.MCPConfig{
			BinaryPath: cfg.BinaryPath,
			DBPath:     cfg.DBPath,
		},
		WorkspacesRoot:   workspacesRoot,
		CLIWritableRoots: cfg.CLIWritableRoots,
	}
	return deps, nil
}

// indexAdapters keys the CLI adapter slice by bare adapter name.
func indexAdapters(adapters []provider.CLIAdapter) map[string]provider.CLIAdapter {
	out := make(map[string]provider.CLIAdapter, len(adapters))
	for _, a := range adapters {
		if a == nil {
			continue
		}
		out[a.Name()] = a
	}
	return out
}

// stripProviderPrefix drops the Nanite registry prefixes ("pty-", "sub-")
// so a launch-profile provider alias resolves to the bare adapter name.
// The bootprofile compiler already normalizes spec.Provider to the bare
// form, so this is defence in depth for callers passing aliases.
func stripProviderPrefix(name string) string {
	for _, p := range []string{"pty-", "sub-"} {
		if strings.HasPrefix(name, p) {
			return strings.TrimPrefix(name, p)
		}
	}
	return name
}

// defaultProfiles is the AgentProfiles resolver for standalone launches.
// A standalone launch is driven entirely by the compiled LaunchSpec
// (provider, env, boot prompt all come from the profile), so the agent
// profile only has to exist — Boot reads DefaultProvider from it, but
// spec.Provider overrides that via Options.Provider. When a store is
// available a named profile is honored; otherwise a synthetic default
// is returned.
type defaultProfiles struct {
	store *store.Store
}

func (d *defaultProfiles) GetOrDefault(name string) (*store.AgentProfile, error) {
	if d.store != nil && name != "" {
		if p, err := d.store.GetAgentBySlug(name); err == nil && p != nil {
			return p, nil
		}
	}
	return &store.AgentProfile{
		ID:              "standalone-launcher-default",
		Name:            "default",
		Slug:            "default",
		DefaultProvider: "claude",
	}, nil
}

// ResolveBinaryPath is a small helper for callers that want the running
// nanite binary path with symlinks evaluated — matches what
// service.BuildAgentDependencies does internally so the planted
// .mcp.json descriptor names a stable path. The CLI subcommand passes
// os.Executable()'s result through this before setting Config.BinaryPath.
func ResolveBinaryPath(exe string) string {
	if exe == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}
