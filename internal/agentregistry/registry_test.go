package agentregistry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"gopkg.in/yaml.v3"
)

// writeCatalog materializes a minimal boot-profile catalog with one
// providers/ entry that ingests as a runtime-binding record.
func writeRuntimeBindingCatalog(t *testing.T, runnerID string) string {
	t.Helper()
	root := t.TempDir()
	provDir := filepath.Join(root, "providers")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	// A providers/ file ingests under the runtime-binding kind. Its body
	// is a RuntimeBindingContract that ResolveRuntimeBinding decodes.
	contract := agentlaunch.RuntimeBindingContract{
		Meta: agentlaunch.RegistryContractMeta{
			Ref: agentlaunch.RegistryObjectRef{
				Kind: agentlaunch.RegistryKindRuntimeBinding,
				Name: runnerID,
			},
			SchemaVersion: agentlaunch.RuntimeBindingSchemaVersionV1,
			Interface:     agentlaunch.RuntimeBindingInterfaceV1,
		},
		Binding: agentlaunch.RuntimeBinding{
			Provider:    "claude",
			RuntimeKind: agentlaunch.RuntimeStreamingStdio,
		},
	}
	raw, err := yaml.Marshal(contract)
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}
	// The file-backed registrar keys the record by the `id` field.
	body := "id: " + runnerID + "\n" + string(raw)
	if err := os.WriteFile(filepath.Join(provDir, runnerID+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write provider: %v", err)
	}
	return root
}

// TestBuild_InertWhenNoCatalog confirms an empty catalog root yields a
// usable-but-inert registry — the D1 offline-safe contract.
func TestBuild_InertWhenNoCatalog(t *testing.T) {
	reg := Build("", "", nil)
	if reg == nil {
		t.Fatal("Build returned nil")
	}
	if reg.Registrar() == nil {
		t.Error("inert registry has no registrar")
	}
	if reg.CatalogRoot() != "" {
		t.Errorf("CatalogRoot = %q, want empty", reg.CatalogRoot())
	}
}

// TestResolveRuntimeBinding_RegistryPrimary proves registry-primary
// resolution: a runtime-binding registered in the catalog resolves
// through the registrar, NOT the fallback.
func TestResolveRuntimeBinding_RegistryPrimary(t *testing.T) {
	root := writeRuntimeBindingCatalog(t, "claude")
	reg := Build(root, "", nil)

	// A deliberately-wrong fallback so a registry hit is distinguishable
	// from a fallback hit.
	fallback := agentlaunch.RuntimeBinding{
		Provider:    "FALLBACK-PROVIDER",
		RuntimeKind: agentlaunch.RuntimeSubprocess,
	}
	got, err := reg.ResolveRuntimeBinding("claude", fallback, nil)
	if err != nil {
		t.Fatalf("ResolveRuntimeBinding: %v", err)
	}
	if got.Provider != "claude" {
		t.Errorf("Provider = %q, want claude (registry-primary, not fallback)", got.Provider)
	}
}

// TestResolveRuntimeBinding_FallbackOnNotFound proves the §4.1 explicit
// fallback: when the registry has no binding for the runner, the
// caller-supplied spec/profile default is used.
func TestResolveRuntimeBinding_FallbackOnNotFound(t *testing.T) {
	root := writeRuntimeBindingCatalog(t, "claude")
	reg := Build(root, "", nil)

	fallback := agentlaunch.RuntimeBinding{
		Provider:    "codex",
		RuntimeKind: agentlaunch.RuntimeStreamingStdio,
	}
	// "codex" has no registered runtime-binding — must degrade to fallback.
	got, err := reg.ResolveRuntimeBinding("codex", fallback, nil)
	if err != nil {
		t.Fatalf("ResolveRuntimeBinding: %v", err)
	}
	if got.Provider != "codex" {
		t.Errorf("Provider = %q, want codex (fallback)", got.Provider)
	}
}

// downRegistrar is a Registrar whose every Handle call fails — it
// simulates an unreachable directory.
type downRegistrar struct{}

func (downRegistrar) Handle(agentlaunch.RegistryEnvelope) (agentlaunch.RegistryResponse, error) {
	return agentlaunch.RegistryResponse{}, errors.New("directory unreachable")
}

// TestResolveRuntimeBinding_DegradesWhenRegistryDown is the D1
// regression: registry-primary resolution must DEGRADE cleanly to the
// file/spec fallback when the registry is unavailable. A DegradingRegistrar
// over a down inner registrar with an empty cache surfaces
// ErrRegistryCacheMiss, which ResolveRuntimeBinding treats as a
// degrade-to-fallback condition, NOT a hard failure.
func TestResolveRuntimeBinding_DegradesWhenRegistryDown(t *testing.T) {
	cache := agentlaunch.NewLastKnownGoodCache()
	degrading := agentlaunch.NewDegradingRegistrar(downRegistrar{}, cache)
	reg := &Registry{
		reg: degrading,
		descriptor: agentlaunch.RegistryRegistrar{
			Mode:     agentlaunch.RegistrarModeFileBacked,
			FileRoot: t.TempDir(),
		},
	}

	fallback := agentlaunch.RuntimeBinding{
		Provider:    "claude",
		RuntimeKind: agentlaunch.RuntimeStreamingStdio,
	}
	got, err := reg.ResolveRuntimeBinding("claude", fallback, nil)
	if err != nil {
		t.Fatalf("ResolveRuntimeBinding degraded path returned error: %v", err)
	}
	if got.Provider != "claude" {
		t.Errorf("Provider = %q, want claude (degraded → fallback)", got.Provider)
	}
}

// TestRegisterAgentSource_HandleOnly is the C1 D2 done-criterion: the
// agent-source registration must carry the resolver HANDLE only — the
// registry record (and the contract file it points at) must NEVER hold
// agent profile bodies (system_prompt / tools / modes).
func TestRegisterAgentSource_HandleOnly(t *testing.T) {
	reg := Build("", "", nil)
	handleDir := t.TempDir()
	loopbackURL := "http://127.0.0.1:8090/api/tools/call"

	if err := RegisterAgentSource(reg, loopbackURL, handleDir, nil); err != nil {
		t.Fatalf("RegisterAgentSource: %v", err)
	}

	// Query the registrar for the agent-source record.
	resp, err := reg.Registrar().Handle(agentlaunch.RegistryEnvelope{
		Version:    agentlaunch.RegistryEnvelopeVersionV1,
		Resolution: agentlaunch.RegistryResolutionLocalFirst,
		Operation:  agentlaunch.RegistryOperationQuery,
		Registrar:  reg.Descriptor(),
		Query: &agentlaunch.QueryPayload{
			Kinds: []agentlaunch.RegistryKind{agentlaunch.RegistryKindAgentSource},
			Name:  AgentSourceName,
		},
	})
	if err != nil {
		t.Fatalf("query agent-source: %v", err)
	}
	if len(resp.Records) != 1 {
		t.Fatalf("got %d agent-source records, want 1", len(resp.Records))
	}
	rec := resp.Records[0]

	// D2: the RegistrationRecord is a handle — meta + a file pointer.
	// It has no field that could carry a profile body, by type.
	if rec.Meta.Ref.Kind != agentlaunch.RegistryKindAgentSource {
		t.Errorf("record kind = %q, want agent-source", rec.Meta.Ref.Kind)
	}
	if rec.Source.FilePath == "" {
		t.Fatal("record has no source file pointer")
	}

	// D2: the file the pointer references is an AgentSourceContract
	// carrying a ResolverHandle — and NOTHING that resembles an agent
	// body. Decode it and assert the shape, then string-scan the raw
	// bytes for profile-body field names as a belt-and-suspenders check.
	raw, err := os.ReadFile(rec.Source.FilePath)
	if err != nil {
		t.Fatalf("read contract file: %v", err)
	}
	var contract agentlaunch.AgentSourceContract
	if err := yaml.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("decode agent-source contract: %v", err)
	}
	if err := contract.Validate(); err != nil {
		t.Fatalf("agent-source contract invalid: %v", err)
	}
	if contract.Resolver.Protocol != agentlaunch.ResolverProtocolHTTP {
		t.Errorf("resolver protocol = %q, want http", contract.Resolver.Protocol)
	}
	if contract.Resolver.Target != loopbackURL {
		t.Errorf("resolver target = %q, want %q", contract.Resolver.Target, loopbackURL)
	}
	if contract.Resolver.Operation != agentSourceResolveOperation {
		t.Errorf("resolver operation = %q, want %q", contract.Resolver.Operation, agentSourceResolveOperation)
	}

	// D2 belt-and-suspenders: no profile-body field names anywhere in
	// the serialized contract. The directory holds a handle, not content.
	body := string(raw)
	for _, forbidden := range []string{"system_prompt", "tool_permissions", "modes:"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("D2 violation: agent-source contract file contains %q — the directory must hold the handle, not the agent body", forbidden)
		}
	}
}

// TestRegisterAgentSource_FailureIsReturnedNotFatal confirms a bad
// loopback URL surfaces an error the caller can log-and-continue on
// (D1: registry registration is never mandatory for startup).
func TestRegisterAgentSource_FailureIsReturnedNotFatal(t *testing.T) {
	reg := Build("", "", nil)
	if err := RegisterAgentSource(reg, "", t.TempDir(), nil); err == nil {
		t.Fatal("expected an error for an empty loopback URL")
	}
}
