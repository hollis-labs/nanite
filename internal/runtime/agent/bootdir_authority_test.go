package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBootCustodyIsExactCancelableAndSealed(t *testing.T) {
	root, authorize, seal := authorizedPlantRoot(t)
	defer seal()
	if _, err := authorize(t.Context(), filepath.Join(root, "other")); !errors.Is(err, ErrArtifactRefreshUnavailable) {
		t.Fatalf("foreign path: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := authorize(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled custody: %v", err)
	}
	authority, err := authorize(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	if !authority.Inactive || !authority.PrivateCustody || authority.Input.Root.Path != root {
		t.Fatal("fresh root lacks real custody")
	}
	for _, path := range []string{root, authority.Input.Resources.LockRoot.Path} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private root %s: %v %v", path, info, err)
		}
	}
	seal()
	if _, err := authorize(t.Context(), root); !errors.Is(err, ErrArtifactRefreshUnavailable) {
		t.Fatalf("sealed custody: %v", err)
	}
}

func TestBootSetupFailureRetainsRootAndEvidence(t *testing.T) {
	_, err := (claudeLayout{}).Setup(SetupParams{SessionID: "failed-custody", Hooks: []BootDirHook{{Name: "invalid", Event: HookEvent("unsupported")}}})
	var failure *BootArtifactFailure
	if !errors.As(err, &failure) || failure.BootDir == "" {
		t.Fatalf("failure lost root: %v", err)
	}
	if _, err := os.Stat(failure.BootDir); err != nil {
		t.Fatalf("failed root removed: %v", err)
	}
	if _, ok := BootArtifactEvidence(failure.BootDir); !ok {
		t.Fatal("failed materialization evidence lost")
	}
	if _, err := (claudeLayout{}).Populate(failure.BootDir, SetupParams{}); !errors.Is(err, ErrArtifactRefreshUnavailable) {
		t.Fatalf("failed bound root reused: %v", err)
	}
}
