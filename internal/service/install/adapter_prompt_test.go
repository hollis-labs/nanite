package install

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestPromptAdapterSelection_DetectedAccept(t *testing.T) {
	in := strings.NewReader("y\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected:      []string{"claude", "codex"},
		Current:       []string{"claude", "codex"},
		IsReconfigure: false,
		Stdin:         in,
		Stdout:        &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Errorf("got %v, want [claude codex]", got)
	}
	if !strings.Contains(out.String(), "Detected") {
		t.Errorf("expected detected message in output: %q", out.String())
	}
}

func TestPromptAdapterSelection_DetectedDecline(t *testing.T) {
	in := strings.NewReader("n\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: []string{"claude"},
		Current:  []string{"claude"},
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want []", got)
	}
}

func TestPromptAdapterSelection_DetectedEditFallsThroughToList(t *testing.T) {
	// "e" → fall through to numbered list → "1 3" picks claude and gemini
	in := strings.NewReader("e\n1 3\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: []string{"claude", "codex"},
		Current:  []string{"claude", "codex"},
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Numbered list is the canonical userSelectableAdapters order.
	if !reflect.DeepEqual(got, []string{"claude", "gemini"}) {
		t.Errorf("got %v, want [claude gemini]", got)
	}
}

func TestPromptAdapterSelection_FreshNoDetection_Empty(t *testing.T) {
	// Detection found nothing, user enters empty → []
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: nil,
		Current:  nil,
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want []", got)
	}
}

func TestPromptAdapterSelection_FreshNoDetection_PicksTwo(t *testing.T) {
	in := strings.NewReader("2 4\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: nil,
		Current:  nil,
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	// userSelectableAdapters order: claude(1), codex(2), gemini(3), opencode(4)
	if !reflect.DeepEqual(got, []string{"codex", "opencode"}) {
		t.Errorf("got %v, want [codex opencode]", got)
	}
}

func TestPromptAdapterSelection_Reconfigure_EmptyKeepsCurrent(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected:      []string{"claude", "codex"},
		Current:       []string{"claude", "gemini"},
		IsReconfigure: true,
		Stdin:         in,
		Stdout:        &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"claude", "gemini"}) {
		t.Errorf("got %v, want current [claude gemini]", got)
	}
	if !strings.Contains(out.String(), "*") {
		t.Errorf("expected * markers in output: %q", out.String())
	}
}

func TestPromptAdapterSelection_Reconfigure_ReplacesList(t *testing.T) {
	in := strings.NewReader("3\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected:      []string{"claude"},
		Current:       []string{"claude", "codex"},
		IsReconfigure: true,
		Stdin:         in,
		Stdout:        &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"gemini"}) {
		t.Errorf("got %v, want [gemini]", got)
	}
}

func TestPromptAdapterSelection_InvalidThenValid(t *testing.T) {
	// "abc" is invalid → re-prompt → "1" picks claude
	in := strings.NewReader("abc\n1\n")
	var out bytes.Buffer
	got, err := promptAdapterSelection(promptInput{
		Detected: nil,
		Current:  nil,
		Stdin:    in,
		Stdout:   &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"claude"}) {
		t.Errorf("got %v, want [claude]", got)
	}
	if !strings.Contains(out.String(), "invalid") {
		t.Errorf("expected 'invalid' warning: %q", out.String())
	}
}
