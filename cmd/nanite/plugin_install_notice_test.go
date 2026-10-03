package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin"
)

func TestFirstInstallPrintsPersistentContextWarning(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "approval")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = input.WriteString("nanite.pins\n"); err != nil {
		t.Fatal(err)
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	originalInput, originalOutput := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = input, output
	t.Cleanup(func() { os.Stdin, os.Stdout = originalInput, originalOutput; _ = input.Close(); _ = output.Close() })
	review := plugin.InstallReview{ID: "nanite.pins", Name: "Pins", Version: "0.2.0", HostNotice: "Plugin nanite.pins may add text to the system prompt; pins: Pinned Context -> pins_list", Capabilities: nil}
	digest, err := reviewPluginInstall(context.Background(), review, nil)
	os.Stdin, os.Stdout = originalInput, originalOutput
	if err != nil || digest != review.Digest() {
		t.Fatal("first-install approval failed", digest, err)
	}
	if _, err = output.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	warning := "Persistent context warning: " + review.HostNotice
	if !strings.Contains(string(raw), warning) || strings.Index(string(raw), warning) > strings.Index(string(raw), "Type nanite.pins to approve") {
		t.Fatal("first-install warning not shown before approval", string(raw))
	}
}
