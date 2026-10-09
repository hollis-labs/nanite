package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/substrate/harness/agentlaunch"
	plant "github.com/hollis-labs/substrate/harness/agentlaunch/planting"
	"github.com/hollis-labs/substrate/harness/workspace"
	"github.com/hollis-labs/substrate/harness/workspace/local"
)

var ErrArtifactRefreshUnavailable = errors.New("artifact refresh unavailable")

// ArtifactRefreshUnavailable is the interim published-contract boundary. A
// timing assumption between turns is not proof that a provider stopped reading
// its bound root. No current binding or artifact is changed on this refusal.
type ArtifactRefreshUnavailable struct{ Provider, Operation string }

func (e *ArtifactRefreshUnavailable) Error() string {
	return fmt.Sprintf("agent: %s %s: %v; restart into a fresh private boot root", e.Provider, e.Operation, ErrArtifactRefreshUnavailable)
}
func (e *ArtifactRefreshUnavailable) Unwrap() error { return ErrArtifactRefreshUnavailable }

// BootArtifactFailure retains the root and partial materialization handle. A
// failed operation is not proof that deleting the root would be safe.
type BootArtifactFailure struct {
	BootDir string
	Result  plant.PlantResult
	Cause   error
}

func (e *BootArtifactFailure) Error() string {
	return fmt.Sprintf("agent: boot artifacts retained at %s: %v", e.BootDir, e.Cause)
}
func (e *BootArtifactFailure) Unwrap() error { return e.Cause }

var bootArtifactEvidence sync.Map

func BootArtifactEvidence(root string) (plant.PlantResult, bool) {
	value, ok := bootArtifactEvidence.Load(root)
	if !ok {
		return plant.PlantResult{}, false
	}
	return value.(plant.PlantResult), true
}

// makeAuthorizedBootDir creates the actual local, private resources before
// issuing a one-operation grant. Only fresh Setup calls obtain this port. The
// closure cannot authorize another pathname or survive returning the binding
// to a provider. TMPDIR must provide Nanite's supported local filesystem
// contract; this port does not claim support for arbitrary remote filesystems.
func makeAuthorizedBootDir(provider string, params SetupParams) (string, agentlaunch.ArtifactAuthorizer, func(), error) {
	rootPath, err := makeBootDir(provider, params)
	if err != nil {
		return "", nil, nil, err
	}
	controlPath, err := os.MkdirTemp("", "nanite-boot-control-*")
	if err != nil {
		return "", nil, nil, &BootArtifactFailure{BootDir: rootPath, Cause: err}
	}
	operation := "nanite-boot-" + uuid.NewString()
	root := workspace.RootRef{ID: operation, Path: rootPath, AllowedBase: filepath.Dir(rootPath), Owner: "nanite", Provenance: "nanite:bootdir"}
	control := workspace.RootRef{ID: operation + "-control", Path: controlPath, AllowedBase: filepath.Dir(controlPath), Owner: "nanite", Provenance: "nanite:bootdir"}
	resources := workspace.Resources{Roots: []workspace.RootRef{root}, LockRoot: control, LockNamespace: controlPath, Capabilities: []workspace.Capability{workspace.CanonicalRoots, workspace.MutationLocks}, Grants: []workspace.EffectGrant{{Kind: workspace.ArtifactEffect, RootID: root.ID, AuthorizationID: operation, Version: "nanite-private-boot-v1"}}}
	var sealed atomic.Bool
	observe := func(ctx context.Context) (workspace.Observations, error) {
		if err := ctx.Err(); err != nil {
			return workspace.Observations{}, err
		}
		if sealed.Load() {
			return workspace.Observations{}, &ArtifactRefreshUnavailable{Provider: provider, Operation: "observe bound root"}
		}
		now := time.Now().UTC()
		evidence := workspace.Observations{At: now, ExpiresAt: now.Add(time.Minute), Capabilities: resources.Capabilities}
		for _, ref := range []workspace.RootRef{root, control} {
			inspected, err := workspace.InspectRoot(ref)
			if err != nil {
				return evidence, err
			}
			evidence.Roots = append(evidence.Roots, inspected)
		}
		return evidence, nil
	}
	authorize := func(ctx context.Context, target string) (agentlaunch.ArtifactAuthority, error) {
		if target != rootPath || sealed.Load() {
			return agentlaunch.ArtifactAuthority{}, &ArtifactRefreshUnavailable{Provider: provider, Operation: "authorize bound root"}
		}
		evidence, err := observe(ctx)
		if err != nil {
			return agentlaunch.ArtifactAuthority{}, err
		}
		ports, closePorts, err := local.New(local.Options{OperationID: operation, ControlRoot: control, Resources: resources, LocalFilesystem: true, Evidence: observe, ValidateAuthority: func(ctx context.Context, spec workspace.Spec, actual workspace.Resources) error {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if sealed.Load() || spec.OperationID != operation || spec.Operation != workspace.Prepare || !reflect.DeepEqual(actual, resources) {
				return &ArtifactRefreshUnavailable{Provider: provider, Operation: "validate boot custody"}
			}
			return nil
		}})
		if err != nil {
			return agentlaunch.ArtifactAuthority{}, err
		}
		input := workspace.TreeRequest{OperationID: operation, Root: root, RootMode: 0700, Resources: resources, Observed: evidence}
		return agentlaunch.ArtifactAuthority{Inactive: true, PrivateCustody: true, Input: input, Ports: ports, Close: closePorts}, nil
	}
	return rootPath, authorize, func() { sealed.Store(true) }, nil
}
