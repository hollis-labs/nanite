package workflowhost

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	workflowcompile "github.com/hollis-labs/go-workflow/compile"
	"github.com/hollis-labs/go-workflow/graph"
	"github.com/hollis-labs/go-workflow/stepkind"
	"github.com/hollis-labs/go-workflow/values"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	nanitestore "github.com/hollis-labs/nanite/internal/store"
)

func TestQualificationExactSourceAndCatalogSurviveSQLiteRestart(t *testing.T) {
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	firstSource := []byte("workflow: {id: source-identity, version: v1}\nsteps:\n  - {id: work, kind: nanite-tool, kind_version: v1, config: {tool: noop}}\n")
	secondSource := append([]byte("# exact-byte identity must include this comment\n"), firstSource...)
	first, err := CompileSource(t.Context(), "source-identity.workflow.yaml", firstSource, registry)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileSource(t.Context(), "source-identity.workflow.yaml", secondSource, registry)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceDigest == second.SourceDigest || first.Plan.Digest == second.Plan.Digest {
		t.Fatalf("different exact source collapsed: source=%s/%s plan=%s/%s", first.SourceDigest, second.SourceDigest, first.Plan.Digest, second.Plan.Digest)
	}
	specs, catalogDigest, err := registry.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if catalogDigest != first.StepKindCatalogDigest || !reflect.DeepEqual(specs, first.StepKindCatalog) {
		t.Fatalf("compiled catalog differs from exact registry snapshot: got=%s want=%s", first.StepKindCatalogDigest, catalogDigest)
	}

	path := filepath.Join(t.TempDir(), "exact-material.db")
	productStore, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewWorkflowStateStore(productStore)
	if err != nil {
		t.Fatal(err)
	}
	if recordErr := state.RecordPlanMaterial(t.Context(), first); recordErr != nil {
		t.Fatal(recordErr)
	}
	if closeErr := productStore.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}
	reopenedStore, err := nanitestore.New(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopenedStore.Close(context.Background()) })
	reopened, err := NewWorkflowStateStore(reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.LoadPlanMaterial(t.Context(), first.Plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.SourceContent, firstSource) || loaded.SourceDigest != values.SHA256Digest(firstSource) || loaded.StepKindCatalogDigest != catalogDigest || !reflect.DeepEqual(loaded.StepKindCatalog, specs) {
		t.Fatalf("restarted exact material differs: %+v", loaded)
	}
	mutated := loaded
	mutated.SourceContent = append([]byte(nil), loaded.SourceContent...)
	mutated.SourceContent[0] ^= 1
	if recordErr := reopened.RecordPlanMaterial(t.Context(), mutated); recordErr == nil {
		t.Fatal("source mutation was accepted under an immutable plan digest")
	}
	missingKind := &frozenRegistry{kinds: registry.kinds, specs: append([]stepkind.StepKindSpec(nil), registry.specs[:len(registry.specs)-1]...)}
	if verifyErr := verifyFrozenCatalog(loaded, missingKind); verifyErr == nil {
		t.Fatal("catalog mutation was accepted under an immutable plan digest")
	}
}

func TestQualificationGeneratedGraphUsesCanonicalValidatedIdentity(t *testing.T) {
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	definition := agentworkflow.WorkflowDefinition{Name: "canonical identity", Engine: agentworkflow.EngineHadron, Steps: []agentworkflow.StepDefinition{{ID: "fetch", Kind: agentworkflow.StepKindTool, Config: map[string]any{"tool": "noop", "args": map[string]any{"z": 2, "a": 1}}}}}
	compiled, err := compileWorkflowDefinition(t.Context(), definition, registry)
	if err != nil {
		t.Fatal(err)
	}
	material := compiled.material
	var generated graph.Graph
	if decodeErr := json.Unmarshal(material.SourceContent, &generated); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if validationErr := generated.ValidateEnums(); validationErr != nil {
		t.Fatal(validationErr)
	}
	canonical, err := json.Marshal(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, material.SourceContent) || material.SourceDigest != values.SHA256Digest(canonical) {
		t.Fatal("generated graph source is not canonical JSON bound by its source digest")
	}
	recompiled := workflowcompile.CompileGraph(generated, workflowcompile.GraphCompileOptions{SourceFormat: material.SourceFormat, SourceDigest: material.SourceDigest, Definition: material.Plan.Definition})
	if recompiled.Plan == nil || len(recompiled.Diagnostics) != 0 {
		t.Fatalf("CompileGraph diagnostics = %#v", recompiled.Diagnostics)
	}
	inferred := workflowcompile.InferValueDependencies(recompiled.Plan, workflowcompile.DependencyOptions{})
	if inferred.Plan == nil || len(inferred.Diagnostics) != 0 || inferred.Plan.Digest != material.Plan.Digest {
		t.Fatalf("canonical CompileGraph identity changed: got=%+v want=%s diagnostics=%#v", inferred.Plan, material.Plan.Digest, inferred.Diagnostics)
	}
}
