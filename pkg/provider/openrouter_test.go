package provider

import (
	"context"
	"net/http"
	"testing"
)

func TestOpenRouter_NoAPIKey(t *testing.T) {
	o := &OpenRouter{apiKey: "", httpClient: http.DefaultClient}
	_, err := o.StreamChat(context.Background(), "", nil, "")
	if err == nil {
		t.Error("expected error for missing API key")
	}
	_, err = o.Complete(context.Background(), "", nil, "")
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestOpenRouter_SetAPIKey(t *testing.T) {
	o := NewOpenRouter()
	o.SetAPIKey("my-key")
	if o.apiKey != "my-key" {
		t.Errorf("expected my-key, got %s", o.apiKey)
	}
}

func TestOpenRouter_Capabilities(t *testing.T) {
	o := NewOpenRouter()
	caps := o.Capabilities()
	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON")
	}
	if !caps.SupportsImageInput {
		t.Error("expected SupportsImageInput")
	}
	if caps.SupportsToolCalling {
		t.Error("expected no SupportsToolCalling")
	}
	if caps.MaxTokens != 0 {
		t.Errorf("expected MaxTokens 0, got %d", caps.MaxTokens)
	}
	if caps.ContextWindowSize != 200000 {
		t.Errorf("expected ContextWindowSize 200000, got %d", caps.ContextWindowSize)
	}
}

// TestOpenRouter_AttributionHeaders ensures the HTTP-Referer and X-Title
// headers configured via env are captured by the constructor so the SDK
// client will forward them on each request.
func TestOpenRouter_AttributionHeaders(t *testing.T) {
	t.Setenv("OPENROUTER_HTTP_REFERER", "https://example.test/nanite")
	t.Setenv("OPENROUTER_X_TITLE", "nanite-test")
	o := NewOpenRouter()
	if o.httpReferer != "https://example.test/nanite" {
		t.Errorf("httpReferer: got %q", o.httpReferer)
	}
	if o.xTitle != "nanite-test" {
		t.Errorf("xTitle: got %q", o.xTitle)
	}
}

// TestOpenRouter_EnsureClient verifies lazy client construction.
func TestOpenRouter_EnsureClient(t *testing.T) {
	o := NewOpenRouter()
	o.ensureClient()
	if o.client != nil {
		t.Error("client should not be constructed without API key")
	}
	o.SetAPIKey("test-key")
	o.ensureClient()
	if o.client == nil {
		t.Error("client should be constructed after SetAPIKey")
	}
	// Subsequent ensureClient calls are no-ops.
	prev := o.client
	o.ensureClient()
	if o.client != prev {
		t.Error("ensureClient should be idempotent")
	}
}
