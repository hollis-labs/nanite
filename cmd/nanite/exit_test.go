package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/secrets/secretstest"
)

func TestPromptWithoutKeyExitsNonzero(t *testing.T) {
	if dir := os.Getenv("NANITE_TEST_NO_KEY_AGENT"); dir != "" {
		secretstest.Install(t)
		os.Args = []string{"nanite", "-C", dir, "-p", "test"}
		main()
		return
	}
	dir := t.TempDir()
	if err := initAgent(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NANITE_TEST_NO_KEY_AGENT", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	for _, key := range []string{"SUBSTRATE_CONFIG", "NANITE_CONFIG", "NANITE_GATEWAY_URL", "NANITE_GATEWAY_KEY_REF", "NANITE_PROVIDER_KEY_ENV", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPromptWithoutKeyExitsNonzero$")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), "resolve anthropic provider key") {
		t.Fatalf("process = %v, stderr = %q", err, stderr.String())
	}
}
