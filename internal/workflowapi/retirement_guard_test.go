package workflowapi

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRetiredWorkflowRuntimeDirectoryIsAbsent(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate workflowapi package")
	}
	legacyDirectory := filepath.Join(filepath.Dir(file), "..", "workflow")
	_, err := os.Stat(legacyDirectory)
	if err == nil {
		t.Fatalf("retired workflow runtime still exists at %s", legacyDirectory)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect retired workflow runtime: %v", err)
	}
}
