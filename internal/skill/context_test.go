package skill

import (
	"strings"
	"testing"
)

func TestResolveDynamicContext_SimpleCommand(t *testing.T) {
	prompt := "Files:\n!`echo hello-world`\nDone."
	result := ResolveDynamicContext(prompt, "")

	if !strings.Contains(result, "hello-world") {
		t.Errorf("expected command output in result, got: %s", result)
	}
	if strings.Contains(result, "!`") {
		t.Errorf("dynamic marker should be replaced, got: %s", result)
	}
}

func TestResolveDynamicContext_NoMarkers(t *testing.T) {
	prompt := "No dynamic context here."
	result := ResolveDynamicContext(prompt, "")

	if result != prompt {
		t.Errorf("expected unchanged prompt, got: %s", result)
	}
}

func TestResolveDynamicContext_FailedCommand(t *testing.T) {
	prompt := "Result: !`nonexistent-command-xyz-12345`"
	result := ResolveDynamicContext(prompt, "")

	if !strings.Contains(result, "<!-- skill context error") {
		t.Errorf("expected error comment for failed command, got: %s", result)
	}
}

func TestResolveDynamicContext_MultipleMarkers(t *testing.T) {
	prompt := "A: !`echo aaa`\nB: !`echo bbb`"
	result := ResolveDynamicContext(prompt, "")

	if !strings.Contains(result, "aaa") || !strings.Contains(result, "bbb") {
		t.Errorf("expected both outputs, got: %s", result)
	}
}
