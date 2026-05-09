package chat

import (
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestResolveAutoRecallConfig_Defaults(t *testing.T) {
	t.Run("nil profile", func(t *testing.T) {
		cfg := ResolveAutoRecallConfig(nil)
		if !cfg.Enabled {
			t.Fatalf("Enabled: want true, got false")
		}
		if cfg.Limit != DefaultAutoRecallLimit {
			t.Fatalf("Limit: want %d, got %d", DefaultAutoRecallLimit, cfg.Limit)
		}
		if cfg.MinConfidence != DefaultAutoRecallMinConfidence {
			t.Fatalf("MinConfidence: want %v, got %v", DefaultAutoRecallMinConfidence, cfg.MinConfidence)
		}
		if cfg.Timeout != DefaultAutoRecallTimeout {
			t.Fatalf("Timeout: want %v, got %v", DefaultAutoRecallTimeout, cfg.Timeout)
		}
	})
	t.Run("empty settings", func(t *testing.T) {
		cfg := ResolveAutoRecallConfig(&store.AgentProfile{Settings: ""})
		if !cfg.Enabled || cfg.Limit != DefaultAutoRecallLimit {
			t.Fatalf("empty settings should yield defaults; got %+v", cfg)
		}
	})
	t.Run("empty JSON object", func(t *testing.T) {
		cfg := ResolveAutoRecallConfig(&store.AgentProfile{Settings: "{}"})
		if !cfg.Enabled || cfg.Limit != DefaultAutoRecallLimit {
			t.Fatalf("{} should yield defaults; got %+v", cfg)
		}
	})
	t.Run("malformed JSON falls back silently", func(t *testing.T) {
		cfg := ResolveAutoRecallConfig(&store.AgentProfile{Settings: "{not json"})
		if !cfg.Enabled {
			t.Fatalf("malformed JSON should yield defaults, got Enabled=false")
		}
	})
}

func TestResolveAutoRecallConfig_ExplicitDisable(t *testing.T) {
	cfg := ResolveAutoRecallConfig(&store.AgentProfile{Settings: `{"auto_recall": false}`})
	if cfg.Enabled {
		t.Fatalf("auto_recall:false should disable; got Enabled=true")
	}
	if cfg.Limit != DefaultAutoRecallLimit {
		t.Fatalf("limit should keep default when not overridden; got %d", cfg.Limit)
	}
}

func TestResolveAutoRecallConfig_ExplicitOverrides(t *testing.T) {
	cfg := ResolveAutoRecallConfig(&store.AgentProfile{
		Settings: `{"auto_recall": true, "auto_recall_limit": 10, "auto_recall_min_confidence": 0.6}`,
	})
	if !cfg.Enabled {
		t.Fatalf("Enabled: want true, got false")
	}
	if cfg.Limit != 10 {
		t.Fatalf("Limit: want 10, got %d", cfg.Limit)
	}
	if cfg.MinConfidence != 0.6 {
		t.Fatalf("MinConfidence: want 0.6, got %v", cfg.MinConfidence)
	}
	if cfg.Timeout != DefaultAutoRecallTimeout {
		t.Fatalf("Timeout: want default, got %v", cfg.Timeout)
	}
}

func TestResolveAutoRecallConfig_InvalidValuesIgnored(t *testing.T) {
	t.Run("non-positive limit ignored", func(t *testing.T) {
		cfg := ResolveAutoRecallConfig(&store.AgentProfile{
			Settings: `{"auto_recall_limit": 0}`,
		})
		if cfg.Limit != DefaultAutoRecallLimit {
			t.Fatalf("limit=0 should fall back to default, got %d", cfg.Limit)
		}
	})
	t.Run("negative limit ignored", func(t *testing.T) {
		cfg := ResolveAutoRecallConfig(&store.AgentProfile{
			Settings: `{"auto_recall_limit": -5}`,
		})
		if cfg.Limit != DefaultAutoRecallLimit {
			t.Fatalf("limit<0 should fall back to default, got %d", cfg.Limit)
		}
	})
	t.Run("min_confidence out of range ignored", func(t *testing.T) {
		cfg := ResolveAutoRecallConfig(&store.AgentProfile{
			Settings: `{"auto_recall_min_confidence": 1.5}`,
		})
		if cfg.MinConfidence != DefaultAutoRecallMinConfidence {
			t.Fatalf("min_confidence>1 should fall back; got %v", cfg.MinConfidence)
		}
	})
	t.Run("min_confidence zero allowed", func(t *testing.T) {
		// 0 is a valid floor (return everything regardless of confidence).
		cfg := ResolveAutoRecallConfig(&store.AgentProfile{
			Settings: `{"auto_recall_min_confidence": 0}`,
		})
		if cfg.MinConfidence != 0 {
			t.Fatalf("min_confidence=0 should be honored, got %v", cfg.MinConfidence)
		}
	})
}

func TestResolveAutoRecallConfig_DoesNotMixWithDebugFlag(t *testing.T) {
	// Settings JSON may carry both debug and auto_recall keys; the resolver
	// must not be tripped by sibling keys.
	cfg := ResolveAutoRecallConfig(&store.AgentProfile{
		Settings: `{"debug": true, "auto_recall": false, "other_key": "ignored"}`,
	})
	if cfg.Enabled {
		t.Fatalf("auto_recall:false should disable; got Enabled=true")
	}
	if cfg.Timeout != DefaultAutoRecallTimeout {
		t.Fatalf("timeout default not applied: %v", cfg.Timeout)
	}
	// Sanity: 2s default timeout
	if DefaultAutoRecallTimeout != 2*time.Second {
		t.Fatalf("default timeout drift: %v", DefaultAutoRecallTimeout)
	}
}
