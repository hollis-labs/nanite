package agent

// bootdir_retire_golden_test.go is the verification harness for CW-20261024-0011
// (retire the per-runtime boot-dir planters in favor of the shared harness
// layout planner). TEST-ONLY: it changes no production code, makes no model
// turn and launches no CLI.
//
// For each of the four runtimes and a matrix of SetupParams it plants the boot
// dir twice and diffs the resulting file trees:
//
//   - CURRENT: bootdirLayoutFor(<runtime>).Setup(params) into a temp dir, then
//     the tree is read back from disk (paths, modes, bytes, symlink targets).
//   - SHARED: the shared planner Nanite would adopt, driven the way a migrated
//     Nanite would drive it: agentlaunch/launcher Compile + Prepare, then
//     agentlaunch/planting.ProjectExecution (the pure projection step of
//     planting.Plant, which renders exactly the artifact tree Plant would
//     materialize, without writing it). The adapter's ProviderProjection is
//     what consumes the layout table (adapters/layout/plan). Two variants:
//     "default"  the library resolver's adapter, nothing Nanite-specific set;
//     "pinned"   WithAdapter(...) carrying the settings Nanite's planters
//     hard-code today (claude permission mode, codex sandbox mode,
//     writable roots and additional directories, opencode agent slug).
//
// WHAT THE SHARED SIDE CANNOT EXPRESS is built here as the closest honest
// equivalent and is marked: Nanite's own files (.sandbox/*, tether identity,
// definition files, skills) ride InjectionSpec.NativeFiles as raw files, and
// the Nanite MCP server rides MCPSpec.Servers. Whatever is left over (hooks,
// the agy HOME farm and private settings.json, codex auth/sessions links,
// argv/env extras) shows up as "only in CURRENT" or in the launch lines,
// because the shared planner has no input for it.
//
// The test compares its output with testdata/bootdir_retire_golden.txt, so a
// dependency bump or a planter change that moves any difference fails loudly
// and the author has to re-read docs/architecture/bootdir-retirement-0011.md.
// Regenerate with:
//
//	go test ./internal/runtime/agent -run TestBootdirRetireGolden -update-bootdir-golden
//
// The differences themselves are EXPECTED (that is the point); only a change in
// them fails the test.

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	claudenf "github.com/hollis-labs/libs/harness/core/adapters/claude/nativefiles"
	codexnf "github.com/hollis-labs/libs/harness/core/adapters/codex/nativefiles"
	"github.com/hollis-labs/libs/harness/core/adapters/layout/plan"
	"github.com/hollis-labs/libs/harness/core/adapters/nativefiles"
	"github.com/hollis-labs/libs/harness/core/adapters/provider"
	"github.com/hollis-labs/libs/harness/core/adapters/registry"
	"github.com/hollis-labs/libs/harness/core/agentlaunch"
	"github.com/hollis-labs/libs/harness/core/agentlaunch/launcher"
	plant "github.com/hollis-labs/libs/harness/core/agentlaunch/planting"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize/artifact"
	"github.com/hollis-labs/libs/harness/core/workspace/render"
	"github.com/hollis-labs/libs/llm/core/contracts/runtimes"
	"github.com/hollis-labs/libs/security/policy/permission"

	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

var updateBootdirGolden = flag.Bool("update-bootdir-golden", false, "rewrite testdata/bootdir_retire_golden.txt from this tree")

const bootdirGoldenPath = "testdata/bootdir_retire_golden.txt"

// goldenEntry is one planted file (or link) in a snapshot.
type goldenEntry struct {
	mode os.FileMode
	data string // file bytes, or the link target for a symlink
	link bool
}

type goldenSnapshot map[string]goldenEntry

// goldenSideEffects is what a launch gets besides files.
type goldenSideEffects struct {
	cwd  string
	env  []string // KEY=VALUE, sorted
	argv []string
	note []string
}

// goldenEnv is the per-subtest world the scenarios build params from.
type goldenEnv struct {
	home    string
	project string
	roots   []string
}

type goldenScenario struct {
	name    string
	posture permission.Mode // applied to the SHARED plan only; the planters have no posture input
	build   func(e goldenEnv) SetupParams
}

const goldenToken = "golden-credential-not-a-secret"

func goldenBase(e goldenEnv) SetupParams {
	return SetupParams{
		SessionID: "sess-golden",
		RunID:     "7",
		Mode:      ModeLongLived,
		AgentProfile: &store.AgentProfile{
			ID: "agt-golden", Name: "Golden Agent", Slug: "golden-agent", Description: "Golden description.",
		},
		SystemPrompt: "You are the golden agent.\nBe brief.",
		BootContent:  "Kickoff: do the golden thing.",
		ProjectDir:   e.project,
	}
}

func goldenMCP(p SetupParams) SetupParams {
	p.MCPConfig = MCPConfig{
		BinaryPath: "/opt/nanite/bin/nanite", DBPath: "/var/nanite/main.db", ServerID: "nanite",
		APIBaseURL:      "http://127.0.0.1:7777",
		AgentCredential: func(string) (string, error) { return goldenToken, nil },
	}
	return p
}

func goldenScenarios() []goldenScenario {
	skillStore := &fakeSkillStore{
		skills: map[string]*store.Skill{
			"enabled-skill":  {Slug: "enabled-skill", ContentHash: skillAddrGood, Enabled: true},
			"disabled-skill": {Slug: "disabled-skill", ContentHash: skillAddrGood, Enabled: false},
		},
		known: map[string][]store.AgentKnownSkill{
			"agt-golden": {
				{AgentID: "agt-golden", SkillName: "enabled-skill", ApprovedContentHash: skillAddrGood},
				{AgentID: "agt-golden", SkillName: "disabled-skill", ApprovedContentHash: skillAddrGood},
			},
		},
	}
	skillVendor := &fakeSkillVendor{files: map[string]skillvendor.FileMap{
		skillAddrGood: {"SKILL.md": []byte("---\nname: x\n---\nbody\n"), "scripts/run.sh": []byte("#!/bin/sh\necho hi\n")},
	}}
	return []goldenScenario{
		{name: "minimal", build: goldenBase},
		{name: "mcp", build: func(e goldenEnv) SetupParams { return goldenMCP(goldenBase(e)) }},
		{name: "mcp-no-credential", build: func(e goldenEnv) SetupParams {
			p := goldenMCP(goldenBase(e))
			p.MCPConfig.AgentCredential = nil
			p.MCPConfig.APIBaseURL = ""
			return p
		}},
		{name: "host-mcp-and-grants", build: func(e goldenEnv) SetupParams {
			p := goldenMCP(goldenBase(e))
			p.HostMCP = &HostMCPLaunch{
				Servers:    []HostMCPServer{{Name: "tether", Command: "tether", Args: []string{"mcp"}, Env: map[string]string{"TETHER_X": "1"}}},
				AllowRules: []string{"mcp__tether__agent_list"},
				Tools:      []string{"agent_list"},
			}
			return p
		}},
		{name: "skills-with-disabled", build: func(e goldenEnv) SetupParams {
			p := goldenMCP(goldenBase(e))
			p.Skills, p.SkillVendor = skillStore, skillVendor
			return p
		}},
		{name: "definition-files", build: func(e goldenEnv) SetupParams {
			p := goldenMCP(goldenBase(e))
			p.Skills, p.SkillVendor = skillStore, skillVendor
			p.AgentProfile.DefinitionFiles = map[string][]byte{
				"agent/refs/policy.md":                []byte("pinned policy\n"),
				"agent/skills/delivered/SKILL.md":     []byte("delivered skill\n"),
				"agent/skills/delivered/scripts/x.sh": []byte("echo x\n"),
				"CLAUDE.md":                           []byte("rogue key outside agent/ must be dropped\n"),
			}
			return p
		}},
		{name: "directories", build: func(e goldenEnv) SetupParams {
			p := goldenMCP(goldenBase(e))
			p.CLIWritableRoots = append([]string{e.project}, e.roots...)
			return p
		}},
		{name: "hooks", build: func(e goldenEnv) SetupParams {
			p := goldenMCP(goldenBase(e))
			p.Hooks = []BootDirHook{{Event: HookStop, Name: "stop.sh", Script: []byte("#!/bin/sh\nexit 0\n")}}
			return p
		}},
		{name: "posture-absent", posture: "", build: goldenBase},
		{name: "posture-default", posture: permission.ModeDefault, build: goldenBase},
		{name: "posture-accept-edits", posture: permission.ModeAcceptEdits, build: goldenBase},
		{name: "posture-plan", posture: permission.ModePlan, build: goldenBase},
		{name: "posture-yolo", posture: permission.ModeYolo, build: goldenBase},
	}
}

// goldenVariants are the shared-planner paths compared against CURRENT:
//
//	projection-default      planting.ProjectExecution, library resolver adapter
//	projection-pinned       the same with WithAdapter carrying Nanite's hard-coded settings
//	canonical-strict        plan.Resolve + workspace/render.Render (the layout-table
//	                        renderer boot.Plan drives), posture exactly as the scenario
//	                        binds it and no host-declared native defaults
//	canonical-hostdefaults  the same, but Nanite's own settings declared as host
//	                        native defaults (what keeps today's bytes) and no posture
var goldenVariants = []string{"projection-default", "projection-pinned", "canonical-strict", "canonical-hostdefaults"}

type goldenRuntime struct {
	id   runtimes.ID
	name string // Nanite provider name
	mode runtimes.Mode
}

func goldenRuntimes() []goldenRuntime {
	return []goldenRuntime{
		{runtimes.Claude, "claude", nativeModes[runtimes.Claude]},
		{runtimes.Codex, "codex", nativeModes[runtimes.Codex]},
		{runtimes.OpenCode, "opencode", nativeModes[runtimes.OpenCode]},
		// agy is not in nativeModes (its mode comes from the registry default).
		{runtimes.Antigravity, "antigravity", runtimes.ModeSubprocessPerTurn},
	}
}

func TestBootdirRetireGolden(t *testing.T) {
	var out bytes.Buffer
	type tally struct{ only, shared, mode, bytes, scenarios, identical int }
	totals := map[string]*tally{}
	var order []string

	fmt.Fprintf(&out, "# CW-20261024-0011 golden: CURRENT planters vs SHARED planner (agentlaunch/planting + adapter ProviderProjection)\n")
	fmt.Fprintf(&out, "# legend: '-' only in CURRENT   '+' only in SHARED   'M' mode differs   'B' bytes differ ('<' current line, '>' shared line)\n\n")

	for _, rt := range goldenRuntimes() {
		for _, variant := range goldenVariants {
			key := string(rt.id) + "/" + variant
			totals[key] = &tally{}
			order = append(order, key)
		}
		for _, sc := range goldenScenarios() {
			rt, sc := rt, sc
			t.Run(string(rt.id)+"/"+sc.name, func(t *testing.T) {
				goldenIsolate(t)
				env := goldenWorld(t)
				params := sc.build(env)

				cur, curFx, curErr := goldenPlantCurrent(t, rt, params, env)
				for _, variant := range goldenVariants {
					tl := totals[string(rt.id)+"/"+variant]
					tl.scenarios++
					fmt.Fprintf(&out, "== %s / %s / %s\n", rt.id, sc.name, variant)
					if curErr != "" {
						fmt.Fprintf(&out, "  current: ERROR %s\n", curErr)
					}
					var sh goldenSnapshot
					var shFx goldenSideEffects
					var shErr string
					if strings.HasPrefix(variant, "canonical") {
						sh, shFx, shErr = goldenPlantCanonical(t, rt, sc, params, env, variant == "canonical-hostdefaults")
					} else {
						sh, shFx, shErr = goldenPlantShared(t, rt, sc, params, env, variant == "projection-pinned")
					}
					if shErr != "" {
						fmt.Fprintf(&out, "  shared: ERROR %s\n", shErr)
					}
					if curErr != "" || shErr != "" {
						out.WriteString("\n")
						continue
					}
					// A disabled skill is never planted on either side. The shared planner has no
					// notion of Enabled, so the filter must stay with the caller
					// (ResolvePlantableSkills); this holds it for every runtime and variant.
					for _, snap := range []goldenSnapshot{cur, sh} {
						for p := range snap {
							if strings.Contains(p, "disabled-skill") {
								t.Errorf("%s/%s/%s: disabled skill planted at %s", rt.id, sc.name, variant, p)
							}
						}
					}
					lines, c := goldenDiff(cur, sh)
					tl.only += c.onlyCur
					tl.shared += c.onlyShared
					tl.mode += c.mode
					tl.bytes += c.bytes
					if c.total() == 0 {
						tl.identical++
					}
					fmt.Fprintf(&out, "  files: current=%d shared=%d  diffs: -%d +%d M%d B%d\n", len(cur), len(sh), c.onlyCur, c.onlyShared, c.mode, c.bytes)
					for _, l := range lines {
						out.WriteString("  " + l + "\n")
					}
					goldenWriteFx(&out, curFx, shFx)
					out.WriteString("\n")
				}
			})
		}
	}

	out.WriteString("# SUMMARY (sum over scenarios): runtime/variant  scenarios  identical  only-current  only-shared  mode  bytes\n")
	for _, key := range order {
		tl := totals[key]
		fmt.Fprintf(&out, "%-24s scenarios=%d identical=%d -%d +%d M%d B%d\n", key, tl.scenarios, tl.identical, tl.only, tl.shared, tl.mode, tl.bytes)
	}

	if *updateBootdirGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bootdirGoldenPath, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(bootdirGoldenPath)
	if err != nil {
		t.Fatalf("%v (run with -update-bootdir-golden)", err)
	}
	if !bytes.Equal(want, out.Bytes()) {
		gotPath := filepath.Join(t.TempDir(), "bootdir_retire_golden.got.txt")
		_ = os.WriteFile(gotPath, out.Bytes(), 0o644)
		t.Fatalf("current-vs-shared planting differences changed; compare %s with %s, re-read docs/architecture/bootdir-retirement-0011.md, then regenerate with -update-bootdir-golden", gotPath, bootdirGoldenPath)
	}
}

// goldenIsolate pins every env switch the planters read so the output is the
// same on every machine.
func goldenIsolate(t *testing.T) {
	t.Helper()
	t.Setenv(StrictMCPEnv, "1")
	t.Setenv(OpenCodeIsolateEnv, "1")
	t.Setenv(PathMentionLaunchRootsEnv, "0")
}

func goldenWorld(t *testing.T) goldenEnv {
	t.Helper()
	home := goldenCanon(t, t.TempDir())
	t.Setenv("HOME", home)
	for _, d := range []string{".ssh", "dev", ".nanite"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := goldenPrivateDir(t)
	r1, r2 := goldenCanon(t, t.TempDir()), goldenCanon(t, t.TempDir())
	return goldenEnv{home: home, project: proj, roots: []string{r1, r2}}
}

func goldenCanon(t *testing.T, p string) string {
	t.Helper()
	c, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func goldenPrivateDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "nanite-golden-proj-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return goldenCanon(t, d)
}

// ---------------------------------------------------------------- CURRENT side

func goldenPlantCurrent(t *testing.T, rt goldenRuntime, params SetupParams, env goldenEnv) (goldenSnapshot, goldenSideEffects, string) {
	t.Helper()
	layout := bootdirLayoutFor(rt.name)
	boot, err := layout.Setup(params)
	if boot != "" {
		t.Cleanup(func() { _ = os.RemoveAll(boot) })
	}
	if err != nil {
		var f *BootArtifactFailure
		if errors.As(err, &f) && f.BootDir != "" {
			t.Cleanup(func() { _ = os.RemoveAll(f.BootDir) })
			return nil, goldenSideEffects{}, goldenNorm(err.Error(), f.BootDir, "", env)
		}
		return nil, goldenSideEffects{}, goldenNorm(err.Error(), boot, "", env)
	}
	snap := goldenSnapshot{}
	goldenReadTree(root{boot, ""}, snap, boot, env, true)
	if rt.id == runtimes.Antigravity {
		// The persistent per-agent private .gemini is outside the boot dir.
		goldenReadTree(root{agyPrivateGeminiDir(boot, params), "agy-private-gemini/"}, snap, boot, env, false)
	}

	amended := layout.AmendEnv(map[string]string{}, boot)
	var envKV []string
	for k, v := range amended {
		envKV = append(envKV, k+"="+goldenNorm(v, boot, "", env))
	}
	sort.Strings(envKV)
	// The argv Nanite adds on top of go-providers' own (selectAdapter's extras).
	var argv []string
	argv = append(argv, strictMCPArgs(rt.id, boot)...)
	argv = append(argv, claudeMCPAllowArgs(rt.id, boot, params.MCPConfig, hostRules(params)...)...)
	argv = append(argv, hostMCPSettingSourcesArgs(rt.id, params.HostMCP != nil)...)
	argv = append(argv, addDirArgs(rt.name, env.project, env.roots)...)
	for i := range argv {
		argv[i] = goldenNorm(argv[i], boot, "", env)
	}
	fx := goldenSideEffects{cwd: goldenNorm(layout.SpawnWorkdir(boot, env.project), boot, "", env), env: envKV, argv: argv}
	return snap, fx, ""
}

func hostRules(p SetupParams) []string {
	if p.HostMCP == nil {
		return nil
	}
	return p.HostMCP.AllowRules
}

type root struct{ dir, prefix string }

func goldenReadTree(r root, snap goldenSnapshot, boot string, env goldenEnv, skipManifest bool) {
	_ = filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == r.dir {
			return nil
		}
		rel, _ := filepath.Rel(r.dir, p)
		rel = filepath.ToSlash(rel)
		if skipManifest && (strings.HasPrefix(rel, ".agentkit") || strings.HasPrefix(rel, strings.SplitN(materialize.ManifestRelPath, "/", 2)[0])) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, lerr := os.Lstat(p)
		if lerr != nil {
			return nil
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			snap[r.prefix+rel] = goldenEntry{link: true, data: goldenNorm(target, boot, "", env)}
			if d.IsDir() {
				return fs.SkipDir
			}
		case d.IsDir():
			// Directories are not compared (the shared tree has no directory
			// entries for plain files); file modes are.
		default:
			b, _ := os.ReadFile(p)
			snap[r.prefix+rel] = goldenEntry{mode: info.Mode().Perm(), data: goldenNorm(string(b), boot, "", env)}
		}
		return nil
	})
}

// ---------------------------------------------------------------- SHARED side

func goldenPlantShared(t *testing.T, rt goldenRuntime, sc goldenScenario, params SetupParams, env goldenEnv, pinned bool) (goldenSnapshot, goldenSideEffects, string) {
	t.Helper()
	ctx := context.Background()

	// Nanite's own files ride NativeFiles as raw files: the closest the shared
	// API permits. Hooks have no input at all (see the doc).
	var native []agentlaunch.NativeFile
	raw := func(rel string, content []byte) {
		native = append(native, agentlaunch.NativeFile{Kind: agentlaunch.NativeFileRaw, RelPath: rel, Content: string(content)})
	}
	for rel, c := range sandboxFiles(params) {
		raw(rel, c)
	}
	if identity := tetherIdentityFile(params); identity != nil {
		raw(".sandbox/tether-identity.md", identity)
	}
	// The same plantable skills the current planter resolves: the shared planner
	// has no notion of grants, approval or Enabled, so the caller filters.
	if params.Skills != nil && params.SkillVendor != nil {
		skills, err := ResolvePlantableSkills(ctx, params.Skills, params.Skills, params.AgentProfile.ID)
		if err != nil {
			return nil, goldenSideEffects{}, err.Error()
		}
		for _, sk := range skills {
			files, err := params.SkillVendor.ReadFiles(sk.ContentHash)
			if err != nil {
				return nil, goldenSideEffects{}, err.Error()
			}
			// SKILL.md's destination comes from the library's own resolution
			// (agentlaunch.SkillRelPath); support files sit beside it.
			skillMD, err := agentlaunch.SkillRelPath(rt.name, rt.mode, sk.Slug)
			if err != nil {
				return nil, goldenSideEffects{}, err.Error()
			}
			for rel, c := range files {
				raw(path.Join(path.Dir(skillMD), rel), c)
			}
		}
	}
	// definitionFilesFor stays Nanite-side logic in a migration (it decides which
	// definition files exist and copies delivered skills to the runtime's skill root).
	for rel, c := range definitionFilesFor(rt.name, params) {
		raw(rel, c)
	}

	var servers []agentlaunch.MCPServerSpec
	if id, cmd, args, envm, err := naniteMCPEntry(params.MCPConfig, params.SessionID, mcpStoreScope(params)); err == nil && id != "" {
		servers = append(servers, agentlaunch.MCPServerSpec{Name: id, Command: cmd, Args: args, Env: envm})
	}
	if params.HostMCP != nil {
		for _, h := range params.HostMCP.Servers {
			servers = append(servers, agentlaunch.MCPServerSpec{Name: h.Name, Command: h.Command, Args: h.Args, Env: h.Env})
		}
	}

	// The default variant maps the profile's display name straight onto the
	// plan; the pinned variant is Nanite-aware and passes the slug opencode
	// keys its agent file on (agentSlug).
	agentName := params.AgentProfile.Name
	if pinned {
		agentName = agentSlug(params)
	}
	plan := agentlaunch.LaunchPlan{
		Project:   agentlaunch.ProjectSpec{ID: "proj", Name: "Project", Root: env.project},
		Agent:     agentlaunch.AgentSpec{ID: params.AgentProfile.ID, Name: agentName},
		Provider:  agentlaunch.ProviderSpec{ID: rt.name, Permission: sc.posture},
		Runtime:   rt.mode,
		Workspace: agentlaunch.WorkspaceSpec{Mode: agentlaunch.WorkspaceTemp, TempPrefix: goldenPrivateDir(t)},
		BootProfile: agentlaunch.BootProfileRef{Inline: &agentlaunch.BootProfileInline{
			BootPrompt: params.SystemPrompt, BootContent: params.BootContent, BootMode: agentlaunch.BootModePlanted,
		}},
		MCP:       agentlaunch.MCPSpec{Servers: servers},
		Injection: agentlaunch.InjectionSpec{NativeFiles: native},
		Mode:      agentlaunch.LaunchInteractive,
	}
	compiled, err := launcher.Compile(ctx, plan)
	if err != nil {
		return nil, goldenSideEffects{}, "compile: " + err.Error()
	}
	prepared, err := launcher.Prepare(ctx, compiled)
	if err != nil {
		return nil, goldenSideEffects{}, "prepare: " + err.Error()
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.PlantedBootDir); _ = os.RemoveAll(prepared.WorkspaceDir) })

	var opts []plant.Option
	if pinned {
		a, err := provider.NewAdapter(rt.id, rt.mode)
		if err != nil {
			return nil, goldenSideEffects{}, err.Error()
		}
		switch ad := a.(type) {
		case *provider.ClaudeAdapter:
			ad.PermissionMode = claudeDefaultPermissionMode
			ad.AdditionalDirectories = params.CLIWritableRoots
		case *provider.CodexAdapter:
			ad.SandboxMode = codexSandboxMode
			ad.WritableRoots = params.CLIWritableRoots
		case *provider.OpencodeAdapter:
			ad.Agent = agentSlug(params)
		}
		bp, ok := a.(provider.BootDirProvider)
		if !ok {
			return nil, goldenSideEffects{}, "adapter has no BootDirSpec"
		}
		opts = append(opts, plant.WithAdapter(bp))
	}
	exec, err := plant.ProjectExecution(ctx, prepared, opts...)
	if err != nil {
		return nil, goldenSideEffects{}, "project: " + err.Error()
	}

	boot := prepared.PlantedBootDir
	snap := goldenSnapshot{}
	for _, e := range exec.Artifacts.Entries {
		if e.Kind != artifact.EntryFile {
			continue
		}
		mode := e.Mode.Perm()
		if mode == 0 {
			mode = 0o644 // the plan package's documented file default
		}
		snap[e.Path] = goldenEntry{mode: mode, data: goldenNorm(string(e.Bytes), boot, prepared.WorkspaceDir, env)}
	}
	fx := goldenSideEffects{cwd: goldenNorm(exec.Bindings.CWD, boot, prepared.WorkspaceDir, env)}
	for k, v := range exec.Bindings.Env {
		if base, inBase := prepared.Env[k]; inBase && base == v.Value {
			continue
		}
		fx.env = append(fx.env, k+"="+goldenNorm(v.Value, boot, prepared.WorkspaceDir, env))
	}
	sort.Strings(fx.env)
	for _, a := range exec.Bindings.Argv {
		fx.argv = append(fx.argv, goldenNorm(a, boot, prepared.WorkspaceDir, env))
	}
	for _, e := range exec.Effects {
		fx.note = append(fx.note, fmt.Sprintf("effect %s -> %s", e.Kind, e.Destination))
	}
	return snap, fx, ""
}

// ---------------------------------------------------------------- diff + render

type goldenCounts struct{ onlyCur, onlyShared, mode, bytes int }

func (c goldenCounts) total() int { return c.onlyCur + c.onlyShared + c.mode + c.bytes }

func goldenDiff(cur, sh goldenSnapshot) ([]string, goldenCounts) {
	var c goldenCounts
	var lines []string
	seen := map[string]bool{}
	var paths []string
	for p := range cur {
		seen[p] = true
		paths = append(paths, p)
	}
	for p := range sh {
		if !seen[p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		a, inCur := cur[p]
		b, inSh := sh[p]
		switch {
		case inCur && !inSh:
			c.onlyCur++
			lines = append(lines, fmt.Sprintf("- %s %s", p, goldenDesc(a)))
		case !inCur && inSh:
			c.onlyShared++
			lines = append(lines, fmt.Sprintf("+ %s %s", p, goldenDesc(b)))
		default:
			if a.link != b.link || a.mode != b.mode {
				c.mode++
				lines = append(lines, fmt.Sprintf("M %s current=%s shared=%s", p, goldenDesc(a), goldenDesc(b)))
			}
			if a.data != b.data {
				c.bytes++
				lines = append(lines, fmt.Sprintf("B %s (%d -> %d bytes)", p, len(a.data), len(b.data)))
				lines = append(lines, goldenLineDiff(a.data, b.data)...)
			}
		}
	}
	return lines, c
}

func goldenDesc(e goldenEntry) string {
	if e.link {
		return "symlink->" + e.data
	}
	return fmt.Sprintf("%04o", e.mode)
}

// goldenLineDiff is a small LCS line diff, capped so the golden stays readable.
func goldenLineDiff(a, b string) []string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case x[i] == y[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	const maxLines = 14
	var out []string
	add := func(s string) {
		if len(out) < maxLines {
			out = append(out, s)
		} else if len(out) == maxLines {
			out = append(out, "      ... (more lines elided)")
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			i++
			j++
		case i < n && (j >= m || lcs[i+1][j] >= lcs[i][j+1]):
			add("      < " + x[i])
			i++
		default:
			add("      > " + y[j])
			j++
		}
	}
	return out
}

func goldenWriteFx(w *bytes.Buffer, cur, sh goldenSideEffects) {
	fmt.Fprintf(w, "  launch: cwd current=%s shared=%s\n", cur.cwd, sh.cwd)
	fmt.Fprintf(w, "  launch: env current-amend=%v shared-binding-env=%v\n", cur.env, sh.env)
	fmt.Fprintf(w, "  launch: argv current-extras=%v\n", cur.argv)
	fmt.Fprintf(w, "  launch: argv shared=%v\n", sh.argv)
	for _, n := range sh.note {
		fmt.Fprintf(w, "  launch: shared %s\n", n)
	}
}

// goldenNorm replaces per-run paths and the session credential with stable
// placeholders. Longer paths first so a prefix never wins.
func goldenNorm(s, boot, workspace string, env goldenEnv) string {
	var pairs []string
	add := func(from, to string) {
		if from == "" {
			return
		}
		pairs = append(pairs, from, to)
		if c, err := filepath.EvalSymlinks(from); err == nil && c != from {
			pairs = append(pairs, c, to)
		}
	}
	add(boot, "<BOOT>")
	add(workspace, "<WORKSPACE>")
	add(env.project, "<PROJECT>")
	for i, r := range env.roots {
		add(r, fmt.Sprintf("<ROOT%d>", i+1))
	}
	add(os.Getenv("CODEX_HOME"), "<HOST_CODEX_HOME>")
	add(env.home, "<HOME>")
	pairs = append(pairs, goldenToken, "<TOKEN>")
	return strings.NewReplacer(pairs...).Replace(s)
}

// ---------------------------------------------------------------- CANONICAL side

// goldenPlantCanonical drives the layout-table planner the way boot.Plan does,
// minus the workspace/definition plumbing: plan.Resolve per field, then
// workspace/render.Render. Instruction bodies are CALLER-SUPPLIED in that API
// (the renderer copies them verbatim), so the current planter's own bodies are
// fed in; only what the planner itself decides can differ. hostDefaults=true
// declares Nanite's hard-coded native settings (claude defaultMode and
// additionalDirectories, codex approval/sandbox/writable_roots) as host
// defaults and binds no posture, because the renderer refuses to mix a bound
// posture with host-supplied native permission settings.
func goldenPlantCanonical(t *testing.T, rt goldenRuntime, sc goldenScenario, params SetupParams, env goldenEnv, hostDefaults bool) (goldenSnapshot, goldenSideEffects, string) {
	t.Helper()
	ctx := context.Background()
	// The boot root is created through Nanite's own custody port, so the tree
	// Render returns can be MATERIALIZED the way the planters do it and read back
	// from disk (real modes, real directories).
	boot, authorize, seal, err := makeAuthorizedBootDir(rt.name, params)
	if boot != "" {
		t.Cleanup(func() { _ = os.RemoveAll(boot) })
	}
	if err != nil {
		t.Fatal(err)
	}
	defer seal()
	slug := agentSlug(params)
	posture := sc.posture
	if hostDefaults {
		posture = ""
	}
	lookup := func(id runtimes.ID, p permission.Mode, m runtimes.Mode) error {
		d, ok := registry.Lookup(string(id))
		if !ok {
			return errors.New("unknown provider")
		}
		_, err := d.PostureFor(p, m)
		return err
	}
	var inputs []render.Input
	resolve := func(field plan.Field, req plan.Requirement, comp map[string]string, c render.Content) string {
		if comp == nil {
			comp = map[string]string{"agent": slug}
		}
		r, err := plan.Resolve(plan.Request{
			Key:         plan.Key{Provider: rt.id, Layer: plan.Boot, Mode: rt.mode, Field: field},
			Requirement: req, Components: comp, Posture: posture, LookupPosture: lookup,
		})
		if err != nil {
			return fmt.Sprintf("plan.Resolve(%s): %v", field, err)
		}
		inputs = append(inputs, render.Input{Resolved: r, Content: c})
		return ""
	}

	// Instructions + kickoff.
	var body []byte
	switch rt.id {
	case runtimes.Claude:
		body = []byte(BuildCLAUDEMD(params.AgentProfile.Name, params.AgentProfile.Description, params.SystemPrompt))
	case runtimes.Codex:
		body = []byte(codexAgentsMD(params))
	case runtimes.Antigravity:
		body = []byte(agyAgentsMD(params))
	case runtimes.OpenCode:
		body = []byte(params.SystemPrompt) // the renderer adds the agent front matter itself
	default:
		return nil, goldenSideEffects{}, "unsupported runtime " + string(rt.id)
	}
	if e := resolve(plan.Instructions, plan.Required, nil, render.Content{Body: body}); e != "" {
		return nil, goldenSideEffects{}, e
	}
	if e := resolve(plan.Kickoff, plan.Required, nil, render.Content{Body: []byte(params.BootContent)}); e != "" {
		return nil, goldenSideEffects{}, e
	}
	for _, f := range []struct {
		field plan.Field
		skip  bool
	}{{plan.Permissions, false}, {plan.Settings, rt.id == runtimes.Antigravity}, {plan.PlantingPlugin, rt.id != runtimes.Antigravity}, {plan.MCP, false}} {
		if f.skip {
			continue
		}
		if e := resolve(f.field, plan.Required, nil, render.Content{}); e != "" {
			return nil, goldenSideEffects{}, e
		}
	}
	if rt.id == runtimes.Codex {
		if e := resolve(plan.Credentials, plan.Required, nil, render.Content{}); e != "" {
			return nil, goldenSideEffects{}, e
		}
	}

	// MCP servers.
	var servers []nativefiles.Server
	addServer := func(name, cmd string, args []string, envm map[string]string) {
		var vars []nativefiles.Variable
		var keys []string
		for k := range envm {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			vars = append(vars, nativefiles.Variable{Name: k, Value: envm[k]})
		}
		servers = append(servers, nativefiles.Server{Name: name, Command: cmd, Args: args, Env: vars})
	}
	if id, cmd, args, envm, err := naniteMCPEntry(params.MCPConfig, params.SessionID, mcpStoreScope(params)); err == nil && id != "" {
		addServer(id, cmd, args, envm)
	}
	if params.HostMCP != nil {
		for _, h := range params.HostMCP.Servers {
			addServer(h.Name, h.Command, h.Args, h.Env)
		}
	}

	// Skills and overlays.
	var overlays []artifact.Entry
	file := func(rel string, c []byte, mode os.FileMode) artifact.Entry {
		return artifact.Entry{Path: rel, Kind: artifact.EntryFile, Mode: mode, Bytes: append([]byte{}, c...)}
	}
	for rel, c := range sandboxFiles(params) {
		overlays = append(overlays, file(rel, c, 0o644))
	}
	if identity := tetherIdentityFile(params); identity != nil {
		overlays = append(overlays, file(".sandbox/tether-identity.md", identity, 0o644))
	}
	pkg := func(slug, source, rev string, files map[string][]byte) string {
		var entries []artifact.Entry
		for rel, c := range files {
			entries = append(entries, file(rel, c, 0o644))
		}
		return resolveSkill(resolve, slug, render.Content{Pin: render.Pin{Source: source, Revision: rev}, Package: artifact.Tree{Entries: entries}})
	}
	if params.Skills != nil && params.SkillVendor != nil {
		skills, err := ResolvePlantableSkills(ctx, params.Skills, params.Skills, params.AgentProfile.ID)
		if err != nil {
			return nil, goldenSideEffects{}, err.Error()
		}
		for _, sk := range skills {
			files, err := params.SkillVendor.ReadFiles(sk.ContentHash)
			if err != nil {
				return nil, goldenSideEffects{}, err.Error()
			}
			if e := pkg(sk.Slug, "nanite.skillvendor", sk.ContentHash, files); e != "" {
				return nil, goldenSideEffects{}, e
			}
		}
	}
	// Definition files: agent/skills/<name>/... are skill packages (so the CLI
	// finds them at its skill root), everything else rides as an overlay.
	delivered := map[string]map[string][]byte{}
	for rel, c := range definitionFiles(params) {
		if rest, ok := strings.CutPrefix(rel, "agent/skills/"); ok {
			if name, file, ok := strings.Cut(rest, "/"); ok {
				if delivered[name] == nil {
					delivered[name] = map[string][]byte{}
				}
				delivered[name][file] = c
			}
		}
		overlays = append(overlays, file(rel, c, 0o644))
	}
	var names []string
	for n := range delivered {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if e := pkg(n, "nanite.definition", "golden", delivered[n]); e != "" {
			return nil, goldenSideEffects{}, e
		}
	}

	req := render.Request{
		Provider: rt.id, Layer: plan.Boot, Mode: rt.mode, Agent: slug, DefinitionName: slug,
		Roots:  map[plan.Root]string{plan.RootBoot: boot, plan.RootProject: env.project, plan.RootHome: env.home},
		Inputs: inputs, Native: render.NativeInputs{Servers: servers}, Overlays: overlays,
	}
	switch rt.id {
	case runtimes.Claude:
		var slots []nativefiles.Slot
		if hs, err := claudeHookSettings(boot, params.Hooks); err != nil {
			return nil, goldenSideEffects{}, err.Error()
		} else if len(hs) > 0 {
			slots = append(slots, nativefiles.Slot{Key: "hooks", Value: hs})
		}
		req.Native.Claude.Slots = slots
		if hostDefaults {
			req.Native.Claude.Permission = &claudenf.Permission{DefaultMode: claudeDefaultPermissionMode, AdditionalDirectories: params.CLIWritableRoots}
		}
		if len(params.Hooks) > 0 {
			for _, h := range params.Hooks {
				req.Overlays = append(req.Overlays, file(path.Join(hookDirRelPath("claude"), h.Name), h.Script, 0o700))
			}
		}
	case runtimes.Codex:
		req.Native.Codex.Encoding = codexnf.BootEncoding
		if hostDefaults {
			req.Native.Codex.ApprovalPolicy = "never"
			req.Native.Codex.SandboxMode = codexSandboxMode
			req.Native.Codex.WritableRoots = params.CLIWritableRoots
		}
	default: // opencode and agy take no typed native input here
	}
	if len(params.Hooks) > 0 && rt.id != runtimes.Claude {
		return nil, goldenSideEffects{}, "(hooks: the planter API has no hook input for this runtime; CURRENT refuses it)"
	}

	res, err := render.Render(req)
	if err != nil {
		return nil, goldenSideEffects{}, goldenNorm(err.Error(), boot, "", env)
	}
	if _, err := (plant.SharedPlanter{Authorize: authorize}).Plant(ctx, boot, plant.PlantSpec{Artifacts: res.Tree}); err != nil {
		return nil, goldenSideEffects{}, goldenNorm("materialize: "+err.Error(), boot, "", env)
	}
	snap := goldenSnapshot{}
	goldenReadTree(root{boot, ""}, snap, boot, env, true)
	fx := goldenSideEffects{cwd: goldenNorm(res.Binding.CWD, boot, "", env)}
	for k, v := range res.Binding.Environment {
		fx.env = append(fx.env, k+"="+goldenNorm(v, boot, "", env))
	}
	sort.Strings(fx.env)
	for _, a := range res.Binding.Argv {
		fx.argv = append(fx.argv, goldenNorm(a, boot, "", env))
	}
	for _, e := range res.Effects {
		fx.note = append(fx.note, fmt.Sprintf("effect %s link %s (%s)", e.Field, e.Path, e.CredentialPolicy))
	}
	for _, d := range res.Diagnostics {
		fx.note = append(fx.note, fmt.Sprintf("diagnostic %s %s: %s", d.Class, d.Code, d.Reason))
	}
	if res.Binding.Posture != nil {
		fx.note = append(fx.note, fmt.Sprintf("posture reference %q", res.Binding.Posture.Posture))
	}
	return snap, fx, ""
}

// resolveSkill resolves one skill package row for slug (the {name} component).
func resolveSkill(resolve func(plan.Field, plan.Requirement, map[string]string, render.Content) string, slug string, c render.Content) string {
	return resolve(plan.Skills, plan.Optional, map[string]string{"name": slug}, c)
}
