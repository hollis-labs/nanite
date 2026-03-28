package provider

import "testing"

func TestCopilotAdapter_Name(t *testing.T) {
	a := NewCopilotAdapter()
	if a.Name() != "copilot" {
		t.Errorf("expected copilot, got %s", a.Name())
	}
}

func TestCopilotAdapter_BuildArgs(t *testing.T) {
	a := NewCopilotAdapter()
	args := a.BuildArgs("explain pointers", "", "")
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}
	if args[0] != "copilot" || args[1] != "explain" || args[2] != "explain pointers" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestCopilotAdapter_ParseLine(t *testing.T) {
	a := NewCopilotAdapter()

	t.Run("empty line", func(t *testing.T) {
		events, err := a.ParseLine([]byte(""))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Errorf("expected 0 events, got %d", len(events))
		}
	})

	t.Run("decorative line skipped", func(t *testing.T) {
		events, err := a.ParseLine([]byte("Synthesizing your answer..."))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Errorf("expected 0 events for synthesizing line, got %d", len(events))
		}
	})

	t.Run("content line emits delta", func(t *testing.T) {
		events, err := a.ParseLine([]byte("Pointers store memory addresses."))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].Type != "delta" {
			t.Errorf("expected delta, got %s", events[0].Type)
		}
	})
}
