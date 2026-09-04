package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/hollis-labs/go-workflow/graph"
	workflowruntime "github.com/hollis-labs/go-workflow/runtime"
	"github.com/hollis-labs/go-workflow/values"
	"github.com/hollis-labs/go-workflow/verification"
)

// HostComponentIdentity freezes every host-owned semantic collaborator that
// is not already carried by the execution plan or StepKind specification.
// The list is data, not a reconstruction hint: recovery compares it with the
// installed pilot contract and refuses to continue after drift.
type HostComponentIdentity struct {
	Role    string `json:"role"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func currentHostContract() ([]HostComponentIdentity, string, error) {
	components := []HostComponentIdentity{
		{Role: "artifact_adapter", Name: ArtifactStoreName, Version: ArtifactStoreVersion},
		{Role: "external_execution_receipt", Name: "nanite-external-execution", Version: "v1"},
		{Role: "failure_disposition", Name: "go-workflow-terminal-default", Version: EngineContractVersion},
		{Role: "recovery_repeat_policy", Name: "nanite-keyed-host-recovery", Version: "v2"},
		{Role: "reuse_authorizer", Name: "disabled-fail-closed", Version: "v1"},
		{Role: "retry_authorizer", Name: "nanite-config-gated", Version: "v1"},
		{Role: "retry_evaluator", Name: "go-workflow-deterministic", Version: EngineContractVersion},
		{Role: "run_policy", Name: "go-workflow-graph-policy", Version: EngineContractVersion},
		{Role: "value_adapter", Name: "nanite-workflowstate-sqlite-values", Version: "v1"},
	}
	return hostContractSnapshot(components)
}

func pilotHostContract() ([]HostComponentIdentity, string, error) {
	components := []HostComponentIdentity{
		{Role: "artifact_adapter", Name: ArtifactStoreName, Version: ArtifactStoreVersion},
		{Role: "failure_disposition", Name: "hadron-terminal-default", Version: PilotContractVersion},
		{Role: "recovery_repeat_policy", Name: "nanite-low-effect-only", Version: "v1"},
		{Role: "reuse_authorizer", Name: "disabled-fail-closed", Version: "v1"},
		{Role: "retry_authorizer", Name: "nanite-config-gated", Version: "v1"},
		{Role: "retry_evaluator", Name: "hadron-deterministic", Version: PilotContractVersion},
		{Role: "run_policy", Name: "hadron-graph-policy", Version: PilotContractVersion},
		{Role: "value_adapter", Name: "nanite-workflowstate-sqlite-values", Version: "v1"},
	}
	return hostContractSnapshot(components)
}

func hostContractSnapshot(components []HostComponentIdentity) ([]HostComponentIdentity, string, error) {
	sort.Slice(components, func(i, j int) bool { return components[i].Role < components[j].Role })
	encoded, err := json.Marshal(components)
	if err != nil {
		return nil, "", fmt.Errorf("marshal workflow host contract: %w", err)
	}
	return components, values.SHA256Digest(encoded), nil
}

func currentVerifierRegistry() (*verification.MemoryRegistry, []verification.VerifierSpec, string, error) {
	snapshot, err := verification.SnapshotRegistry(verification.NewDefaultRegistry())
	if err != nil {
		return nil, nil, "", fmt.Errorf("snapshot go-workflow verifier catalog: %w", err)
	}
	specs := snapshot.List()
	sort.Slice(specs, func(i, j int) bool { return specs[i].Kind < specs[j].Kind })
	encoded, err := json.Marshal(specs)
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal go-workflow verifier catalog: %w", err)
	}
	return snapshot, specs, values.SHA256Digest(encoded), nil
}

func validateVerifierCatalog(specs []verification.VerifierSpec, expectedDigest string) error {
	if len(specs) == 0 {
		return errors.New("verifier catalog snapshot is required")
	}
	for index, spec := range specs {
		if err := spec.Validate(); err != nil {
			return fmt.Errorf("verifier catalog item %d: %w", index, err)
		}
		if index > 0 && specs[index-1].Kind >= spec.Kind {
			return errors.New("verifier catalog must be unique and sorted by exact kind")
		}
	}
	encoded, err := json.Marshal(specs)
	if err != nil {
		return fmt.Errorf("marshal verifier catalog: %w", err)
	}
	if digest := values.SHA256Digest(encoded); digest != expectedDigest {
		return fmt.Errorf("verifier catalog digest mismatch: recorded %q, computed %q", expectedDigest, digest)
	}
	return nil
}

func validateHostContract(contract []HostComponentIdentity, expectedDigest string) error {
	if len(contract) == 0 {
		return errors.New("host contract snapshot is required")
	}
	for index, component := range contract {
		if component.Role == "" || component.Name == "" || component.Version == "" {
			return fmt.Errorf("host contract item %d requires role, name, and version", index)
		}
		if index > 0 && contract[index-1].Role >= component.Role {
			return errors.New("host contract must be unique and sorted by role")
		}
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return fmt.Errorf("marshal host contract: %w", err)
	}
	if digest := values.SHA256Digest(encoded); digest != expectedDigest {
		return fmt.Errorf("host contract digest mismatch: recorded %q, computed %q", expectedDigest, digest)
	}
	return nil
}

func verifyInstalledExecutionIdentity(material PlanMaterial, registry *frozenRegistry) (*verification.MemoryRegistry, error) {
	if err := verifyFrozenCatalog(material, registry); err != nil {
		return nil, err
	}
	verifiers, specs, digest, err := currentVerifierRegistry()
	if err != nil {
		return nil, err
	}
	if !equalWorkflowJSON(specs, material.VerifierCatalog) || digest != material.VerifierCatalogDigest {
		return nil, errors.New("installed go-workflow verifier catalog differs from persisted exact identity")
	}
	contract, contractDigest, err := currentHostContract()
	if registry.pilot {
		contract, contractDigest, err = pilotHostContract()
	}
	if err != nil {
		return nil, err
	}
	if !equalWorkflowJSON(contract, material.HostContract) || contractDigest != material.HostContractDigest {
		return nil, errors.New("installed workflow host contract differs from persisted exact identity")
	}
	return verifiers, nil
}

func equalWorkflowJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

type naniteRepeatPolicy struct{}

func (naniteRepeatPolicy) EvaluateRepeat(_ context.Context, candidate workflowruntime.RepeatCandidate) (workflowruntime.RepeatPolicyDecision, error) {
	lowEffect := true
	for _, effect := range candidate.Effects {
		if effect != graph.EffectRead && effect != graph.EffectCompute {
			lowEffect = false
			break
		}
	}
	if lowEffect {
		return workflowruntime.RepeatPolicyDecision{
			Allow: true, Code: "nanite_low_effect", Reason: "Nanite recovery permits exact read/compute repetition",
		}, nil
	}
	if candidate.Operation == workflowruntime.RepeatCrashRecovery && candidate.IdempotencyKey != "" &&
		(candidate.Spec.Name == StepKindLoop || candidate.Spec.Name == StepKindExternalEngine) {
		return workflowruntime.RepeatPolicyDecision{
			Allow: true, Code: "nanite_keyed_host_recovery",
			Reason: "Nanite recovery permits exact keyed re-entry through a durable host receipt",
		}, nil
	}
	return workflowruntime.RepeatPolicyDecision{
		Code: "nanite_effect_denied", Reason: "Nanite recovery denies consequential effects without an approved keyed host protocol",
	}, nil
}

var _ workflowruntime.RepeatPolicy = naniteRepeatPolicy{}

type naniteRetryAuthorizer struct{}

func (naniteRetryAuthorizer) AuthorizeRetry(ctx context.Context, request workflowruntime.RetryAuthorizationRequest) error {
	if ctx == nil {
		return errors.New("nanite retry authorization requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Spec.Name == StepKindTool {
		if allowed, _ := request.Node.Config["retry_safe"].(bool); !allowed {
			return errors.New("nanite direct-tool retry requires retry_safe=true")
		}
		return nil
	}
	if request.Spec.Name == StepKindLoop || request.Spec.Name == StepKindExternalEngine {
		if request.AttemptStatus != workflowruntime.NodeCrashed || request.IdempotencyKey == "" {
			return errors.New("nanite host retry requires crashed status and an exact idempotency key")
		}
		return nil
	}
	if tools := configStringSliceValue(request.Node.Config, "tools"); len(tools) != 0 {
		return errors.New("nanite LLM/Turn retry is denied when tools are enabled")
	}
	return nil
}

var _ workflowruntime.RetryAuthorizer = naniteRetryAuthorizer{}
