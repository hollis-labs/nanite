package skill

// TASKS/skills/06-build-skill-resolver-and-parameter-binding.md's own
// Done-means:
//   - a skill with a required parameter bound to an existing agent
//     context-resolver row resolves correctly end-to-end (a real
//     ResolveContextBlocks call, not mocked);
//   - a static caller-supplied arg overrides/coexists correctly per this
//     file's chosen precedence rule (static wins);
//   - a missing required parameter with no binding and no static arg
//     produces a clear error, not a panic or a silently-empty value;
//   - nested-dependency address resolution works against a real installed
//     dependency (task 04's test fixture pattern, extended here with a
//     second package it declares a dependency on).

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

const resolverFixturesDir = "testdata/fixtures"

func newResolverTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "resolver-test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newResolverTestVendor(t *testing.T) *skillvendor.Store {
	t.Helper()
	v, err := skillvendor.New(filepath.Join(t.TempDir(), "vendor"))
	if err != nil {
		t.Fatalf("skillvendor.New: %v", err)
	}
	return v
}

// installFixture parses, vendors, and indexes a real skill package
// directory end to end (Parse -> Vendor -> Index) — this file's own,
// deliberately minimal stand-in for internal/skillinstall's fuller
// pipeline (validation, rollback, re-sync), used here only so this
// package's tests can set up a genuinely installed skill without adding a
// test-only dependency from internal/skill onto the sibling
// internal/skillinstall package/testdata.
func installFixture(t *testing.T, idx *store.Store, vendor *skillvendor.Store, dir string) *store.Skill {
	t.Helper()
	def, files, err := ParsePackageDir(dir)
	if err != nil {
		t.Fatalf("ParsePackageDir(%s): %v", dir, err)
	}
	wr, err := vendor.Write(context.Background(), skillvendor.FileMap(files))
	if err != nil {
		t.Fatalf("vendor.Write(%s): %v", dir, err)
	}
	sk := def.ToStoreSkill()
	sk.ID = "" // let CreateSkill mint a real UUID, matching skillinstall.upsertIndex's own fix.
	sk.ContentHash = wr.Address
	if len(def.Dependencies) > 0 {
		// Mirrors internal/skillinstall's own
		// extractDeclaredDependencies/upsertIndex behavior (marshal the
		// declared slug list into store.Skill.DeclaredDependencies) without
		// importing that package as a test-only dependency.
		depsJSON, jerr := json.Marshal(def.Dependencies)
		if jerr != nil {
			t.Fatalf("marshal declared dependencies: %v", jerr)
		}
		sk.DeclaredDependencies = string(depsJSON)
	}
	if err := idx.CreateSkill(sk); err != nil {
		t.Fatalf("CreateSkill(%s): %v", dir, err)
	}
	return sk
}

func TestResolveSkillParameters_DynamicBindingResolvesEndToEnd(t *testing.T) {
	s := newResolverTestStore(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Resolver Test Agent", Slug: "resolver-test-agent", SystemPrompt: "test"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := s.InsertAgentContextResolver(ctx, store.AgentContextResolver{
		AgentID:  agent.ID,
		SlotName: "greeting-slot",
		Kind:     "cmd",
		Run:      "printf hello-from-resolver",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}

	def := Definition{
		Slug: "greeter",
		Parameters: []ParameterSpec{
			{Name: "greeting", Required: true, ResolverSlot: "greeting-slot"},
		},
	}

	got, err := ResolveSkillParameters(ctx, def, agent.ID, nil, "", s)
	if err != nil {
		t.Fatalf("ResolveSkillParameters: %v", err)
	}
	if got["greeting"] != "hello-from-resolver" {
		t.Errorf(`got["greeting"] = %q, want "hello-from-resolver"`, got["greeting"])
	}
}

func TestResolveSkillParameters_StaticArgOverridesDynamicBinding(t *testing.T) {
	s := newResolverTestStore(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Resolver Test Agent 2", Slug: "resolver-test-agent-2", SystemPrompt: "test"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := s.InsertAgentContextResolver(ctx, store.AgentContextResolver{
		AgentID:  agent.ID,
		SlotName: "greeting-slot",
		Kind:     "cmd",
		Run:      "printf hello-from-resolver",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}

	def := Definition{
		Slug: "greeter",
		Parameters: []ParameterSpec{
			{Name: "greeting", Required: true, ResolverSlot: "greeting-slot"},
		},
	}

	got, err := ResolveSkillParameters(ctx, def, agent.ID, map[string]string{"greeting": "hello-from-caller"}, "", s)
	if err != nil {
		t.Fatalf("ResolveSkillParameters: %v", err)
	}
	if got["greeting"] != "hello-from-caller" {
		t.Errorf("static arg should win over a dynamic binding, got %q", got["greeting"])
	}
}

func TestResolveSkillParameters_OptionalUnresolvedParameterIsOmitted(t *testing.T) {
	def := Definition{
		Slug: "greeter",
		Parameters: []ParameterSpec{
			{Name: "note", Required: false},
		},
	}
	got, err := ResolveSkillParameters(context.Background(), def, "some-agent", nil, "", nil)
	if err != nil {
		t.Fatalf("ResolveSkillParameters: %v", err)
	}
	if _, ok := got["note"]; ok {
		t.Errorf("optional, unresolved parameter should be omitted entirely, got %q", got["note"])
	}
}

func TestResolveSkillParameters_MissingRequiredParameterProducesNamedError(t *testing.T) {
	def := Definition{
		Slug: "needs-input",
		Parameters: []ParameterSpec{
			{Name: "target", Required: true},
		},
	}

	got, err := ResolveSkillParameters(context.Background(), def, "some-agent", nil, "", nil)
	if err == nil {
		t.Fatalf("expected an error for a missing required parameter, got a value: %v", got)
	}
	var merr *MissingSkillParameterError
	if !errors.As(err, &merr) {
		t.Fatalf("expected a *MissingSkillParameterError, got %T: %v", err, err)
	}
	if len(merr.Names) != 1 || merr.Names[0] != "target" {
		t.Errorf("MissingSkillParameterError.Names = %v, want [target]", merr.Names)
	}
	if merr.Skill != "needs-input" {
		t.Errorf("MissingSkillParameterError.Skill = %q, want %q", merr.Skill, "needs-input")
	}
}

func TestResolveSkillParameters_MissingRequiredParameterNamesAreDeduplicated(t *testing.T) {
	// Two distinct ParameterSpec entries sharing the same Name can both be
	// Required and both unresolvable (no static arg, no ResolverSlot) — a
	// Definition constructed directly (not through the installer's
	// duplicate-name validation) has no structural guarantee against this.
	// MissingSkillParameterError.Names' own doc comment promises a
	// deduplicated list, so this must produce exactly one entry, not two.
	def := Definition{
		Slug: "needs-input",
		Parameters: []ParameterSpec{
			{Name: "target", Required: true},
			{Name: "target", Required: true},
		},
	}

	_, err := ResolveSkillParameters(context.Background(), def, "some-agent", nil, "", nil)
	var merr *MissingSkillParameterError
	if !errors.As(err, &merr) {
		t.Fatalf("expected a *MissingSkillParameterError, got %T: %v", err, err)
	}
	if len(merr.Names) != 1 || merr.Names[0] != "target" {
		t.Errorf("MissingSkillParameterError.Names = %v, want exactly one entry [target]", merr.Names)
	}
}

func TestResolveSkillParameters_MissingRequiredParameter_UnconfiguredResolverSlot(t *testing.T) {
	// The parameter names a ResolverSlot, but the agent has no resolver
	// row for that slot at all (not merely disabled) — this must still
	// surface as a clear MissingSkillParameterError, not a resolver
	// lookup failure and not a silently-empty value.
	s := newResolverTestStore(t)
	ctx := context.Background()
	agent := &store.AgentProfile{Name: "Resolver Test Agent 3", Slug: "resolver-test-agent-3", SystemPrompt: "test"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	def := Definition{
		Slug: "needs-input",
		Parameters: []ParameterSpec{
			{Name: "target", Required: true, ResolverSlot: "nonexistent-slot"},
		},
	}

	_, err := ResolveSkillParameters(ctx, def, agent.ID, nil, "", s)
	var merr *MissingSkillParameterError
	if !errors.As(err, &merr) {
		t.Fatalf("expected a *MissingSkillParameterError, got %T: %v", err, err)
	}
	if len(merr.Names) != 1 || merr.Names[0] != "target" {
		t.Errorf("MissingSkillParameterError.Names = %v, want [target]", merr.Names)
	}
}

func TestResolveSkillParameters_NoParametersDeclared(t *testing.T) {
	def := Definition{Slug: "no-params"}
	got, err := ResolveSkillParameters(context.Background(), def, "some-agent", nil, "", nil)
	if err != nil {
		t.Fatalf("ResolveSkillParameters: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected an empty map for a skill with no declared parameters, got %v", got)
	}
}

func TestResolveSkillParameters_NeedsBindingButNoResolverStoreConfigured(t *testing.T) {
	def := Definition{
		Slug: "greeter",
		Parameters: []ParameterSpec{
			{Name: "greeting", Required: true, ResolverSlot: "greeting-slot"},
		},
	}
	_, err := ResolveSkillParameters(context.Background(), def, "some-agent", nil, "", nil)
	if err == nil {
		t.Fatal("expected a configuration error when a resolver binding is needed but no AgentContextResolverStore was supplied")
	}
	var merr *MissingSkillParameterError
	if errors.As(err, &merr) {
		t.Fatalf("expected a plain configuration error, not a MissingSkillParameterError: %v", err)
	}
}

func TestResolveSkillParameters_ResolverRowFailureAbortsWholeCall(t *testing.T) {
	// Matches runtimeagent.ResolveContextBlocks's own documented
	// all-or-nothing behavior — a single matching resolver row's failure
	// aborts resolution entirely, not just that one parameter.
	s := newResolverTestStore(t)
	ctx := context.Background()
	agent := &store.AgentProfile{Name: "Resolver Test Agent 4", Slug: "resolver-test-agent-4", SystemPrompt: "test"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := s.InsertAgentContextResolver(ctx, store.AgentContextResolver{
		AgentID:  agent.ID,
		SlotName: "broken-slot",
		Kind:     "cmd",
		Run:      "exit 3",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}

	def := Definition{
		Slug: "greeter",
		Parameters: []ParameterSpec{
			{Name: "greeting", Required: true, ResolverSlot: "broken-slot"},
		},
	}

	_, err := ResolveSkillParameters(ctx, def, agent.ID, nil, "", s)
	if err == nil {
		t.Fatal("expected the broken resolver row's failure to propagate")
	}
	var merr *MissingSkillParameterError
	if errors.As(err, &merr) {
		t.Fatalf("a real resolver failure should not be reported as a missing parameter: %v", err)
	}
}

func TestResolveDependencyAddresses_RealInstalledDependency(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	child := installFixture(t, idx, vendor, filepath.Join(resolverFixturesDir, "resolver-child"))
	parent := installFixture(t, idx, vendor, filepath.Join(resolverFixturesDir, "resolver-parent"))

	if parent.DeclaredDependencies != `["resolver-child"]` {
		t.Fatalf("fixture setup: parent.DeclaredDependencies = %q, want [\"resolver-child\"]", parent.DeclaredDependencies)
	}

	deps, err := ResolveDependencyAddresses(idx, parent.DeclaredDependencies)
	if err != nil {
		t.Fatalf("ResolveDependencyAddresses: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("len(deps) = %d, want 1", len(deps))
	}
	got := deps[0]
	if got.Slug != "resolver-child" {
		t.Errorf("Slug = %q, want %q", got.Slug, "resolver-child")
	}
	if got.Address != child.ContentHash {
		t.Errorf("Address = %q, want the installed dependency's ContentHash %q", got.Address, child.ContentHash)
	}
	if got.Skill.ID != child.ID {
		t.Errorf("Skill.ID = %q, want %q", got.Skill.ID, child.ID)
	}

	// The resolved address is real and independently readable from the
	// vendored store, not just an opaque string.
	files, err := vendor.ReadFiles(got.Address)
	if err != nil {
		t.Fatalf("vendor.ReadFiles(%s): %v", got.Address, err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Error("resolved dependency address's vendored content is missing SKILL.md")
	}
}

func TestResolveDependencyAddresses_NoDependencies(t *testing.T) {
	idx := newResolverTestStore(t)
	deps, err := ResolveDependencyAddresses(idx, "[]")
	if err != nil {
		t.Fatalf("ResolveDependencyAddresses: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("expected no dependencies, got %v", deps)
	}

	deps2, err := ResolveDependencyAddresses(idx, "")
	if err != nil {
		t.Fatalf("ResolveDependencyAddresses(\"\"): %v", err)
	}
	if len(deps2) != 0 {
		t.Errorf("expected no dependencies for an empty string, got %v", deps2)
	}
}

func TestResolveDependencyAddresses_UnresolvedDependencyIsAHardError(t *testing.T) {
	idx := newResolverTestStore(t)
	_, err := ResolveDependencyAddresses(idx, `["ghost-skill"]`)
	if err == nil {
		t.Fatal("expected an error for a declared dependency that isn't installed")
	}
}
