package intent

import (
	"context"
	"errors"
	"testing"
)

// fakeClassifier is a scripted inner classifier for broker tests.
type fakeClassifier struct {
	called bool
	result Result
	err    error
}

func (f *fakeClassifier) Classify(context.Context, Input) (Result, error) {
	f.called = true
	return f.result, f.err
}

func TestBroker_ExplicitOverrideOnShortCircuits(t *testing.T) {
	llm := &fakeClassifier{}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	r, err := b.Classify(context.Background(), Input{
		UserTurn:            "anything",
		AvailableCategories: allCats,
		Override:            OverrideOn,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Hydrate || r.Source != SourceExplicit {
		t.Fatalf("expected explicit hydrate, got %+v", r)
	}
	if llm.called {
		t.Fatalf("LLM should not be called on explicit override")
	}
}

func TestBroker_ExplicitOverrideOffShortCircuits(t *testing.T) {
	llm := &fakeClassifier{}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	r, _ := b.Classify(context.Background(), Input{
		UserTurn:            "run make test please",
		AvailableCategories: allCats,
		Override:            OverrideOff,
	})
	if r.Hydrate || r.Source != SourceExplicit {
		t.Fatalf("OverrideOff must dehydrate via explicit; got %+v", r)
	}
	if llm.called {
		t.Fatalf("LLM should not be called on OverrideOff")
	}
}

func TestBroker_LiteralToolNameMentionHydrates(t *testing.T) {
	llm := &fakeClassifier{}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	r, _ := b.Classify(context.Background(), Input{
		UserTurn:            "please use dev_bash to check",
		AvailableCategories: allCats,
		ToolNames:           []string{"dev_bash", "search_notes"},
	})
	if !r.Hydrate || r.Source != SourceExplicit {
		t.Fatalf("tool-name mention must explicit-hydrate; got %+v", r)
	}
	if llm.called {
		t.Fatalf("LLM should not be reached")
	}
}

func TestBroker_ToolNameRequiresWordBoundary(t *testing.T) {
	llm := &fakeClassifier{}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	// "search_notes" is a tool name; "searching" is not — don't match inside a longer word.
	r, _ := b.Classify(context.Background(), Input{
		UserTurn:            "I was searching for ideas",
		AvailableCategories: allCats,
		ToolNames:           []string{"search"},
	})
	if r.Source == SourceExplicit {
		t.Fatalf("must not match tool name inside a longer word; got %+v", r)
	}
}

func TestBroker_HighConfidenceRulesWin(t *testing.T) {
	llm := &fakeClassifier{result: Result{Hydrate: false, Source: SourceLLM}}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	r, _ := b.Classify(context.Background(), Input{
		UserTurn:            "please execute the script now",
		AvailableCategories: allCats,
	})
	if !r.Hydrate || r.Source != SourceRules {
		t.Fatalf("high-confidence rules should win; got %+v", r)
	}
	if llm.called {
		t.Fatalf("LLM should not be called when rules cross high threshold")
	}
}

func TestBroker_LowConfidenceSkipsLLM(t *testing.T) {
	llm := &fakeClassifier{result: Result{Hydrate: true, Source: SourceLLM}}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	r, _ := b.Classify(context.Background(), Input{
		UserTurn:            "hi there how are you today",
		AvailableCategories: allCats,
	})
	// Rules produce very low confidence — broker should NOT fall through to LLM.
	if r.Hydrate {
		t.Fatalf("low-confidence ambient chat should not hydrate; got %+v", r)
	}
	if llm.called {
		t.Fatalf("LLM should not be called when rules are confidently negative")
	}
}

func TestBroker_AmbiguousZoneFallsThroughToLLM(t *testing.T) {
	llm := &fakeClassifier{result: Result{Hydrate: true, Categories: []string{"search"}, Source: SourceLLM, Reasoning: "LLM said yes"}}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	// "find" is weight 0.5 — between BrokerRulesLow (0.3) and BrokerRulesHigh (0.7).
	r, _ := b.Classify(context.Background(), Input{
		UserTurn:            "find it",
		AvailableCategories: allCats,
	})
	if !llm.called {
		t.Fatalf("ambiguous confidence must invoke LLM")
	}
	if r.Source != SourceLLM {
		t.Fatalf("source: got %q, want %q", r.Source, SourceLLM)
	}
}

func TestBroker_NilLLMReturnsRulesVerdict(t *testing.T) {
	b := NewBrokerClassifier(NewRulesClassifier(), nil)
	// Ambiguous zone, but no LLM wired — should return rules result as-is.
	r, _ := b.Classify(context.Background(), Input{
		UserTurn:            "find it",
		AvailableCategories: allCats,
	})
	if r.Source != SourceRules {
		t.Fatalf("without LLM wired, source must be rules; got %+v", r)
	}
	if r.Hydrate {
		t.Fatalf("single weak match shouldn't hydrate; got %+v", r)
	}
}

func TestBroker_LLMErrorKeepsFailOpenResult(t *testing.T) {
	llm := &fakeClassifier{
		result: failOpen("simulated"),
		err:    errors.New("transport"),
	}
	b := NewBrokerClassifier(NewRulesClassifier(), llm)
	r, err := b.Classify(context.Background(), Input{
		UserTurn:            "find it",
		AvailableCategories: allCats,
	})
	if err != nil {
		t.Fatalf("broker should swallow LLM error when result is fail-open; got %v", err)
	}
	if !r.Hydrate || r.Source != SourceFallback {
		t.Fatalf("must preserve fail-open result on LLM error; got %+v", r)
	}
}
