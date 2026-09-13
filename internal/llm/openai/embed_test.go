package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"
)

func newTestEmbedder(t *testing.T, handler http.HandlerFunc) *Embedder {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewEmbedder("test-key", srv.Client(), option.WithBaseURL(srv.URL+"/"))
}

func TestEmbed_RequestShapeAndResponse(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	e := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],
			"model":"text-embedding-3-small",
			"usage":{"prompt_tokens":5,"total_tokens":5}
		}`))
	})

	res, err := e.Embed(context.Background(), "hello", "text-embedding-3-small")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/embeddings") {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["model"] != "text-embedding-3-small" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["input"] != "hello" {
		t.Errorf("input = %v", gotBody["input"])
	}
	if got := res.Embedding; len(got) != 3 || got[0] != 0.1 || got[2] != 0.3 {
		t.Errorf("embedding = %v", got)
	}
	if res.TokenCount != 5 {
		t.Errorf("token count = %d, want 5", res.TokenCount)
	}
}

func TestEmbed_EmptyModelErrors(t *testing.T) {
	e := NewEmbedder("test-key", nil)
	if _, err := e.Embed(context.Background(), "hello", ""); err == nil {
		t.Fatal("expected error for empty model")
	}
}

func TestEmbedBatch_ArrayInputAndPerRowTokens(t *testing.T) {
	var gotBody map[string]any
	e := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = readJSON(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[
				{"object":"embedding","index":0,"embedding":[0.1,0.2]},
				{"object":"embedding","index":1,"embedding":[0.3,0.4]}
			],
			"model":"m",
			"usage":{"prompt_tokens":10,"total_tokens":10}
		}`))
	})
	res, err := e.EmbedBatch(context.Background(), []string{"a", "b"}, "m")
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	input, ok := gotBody["input"].([]any)
	if !ok || len(input) != 2 || input[0] != "a" || input[1] != "b" {
		t.Errorf("input = %v", gotBody["input"])
	}
	if len(res) != 2 {
		t.Fatalf("len(res) = %d", len(res))
	}
	if res[0].TokenCount != 5 || res[1].TokenCount != 5 {
		t.Errorf("per-row tokens = %d/%d", res[0].TokenCount, res[1].TokenCount)
	}
}

func TestEmbedBatch_EmptyNoCall(t *testing.T) {
	e := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("HTTP must not be called for empty input")
	})
	res, err := e.EmbedBatch(context.Background(), nil, "m")
	if err != nil || res != nil {
		t.Errorf("got res=%v err=%v", res, err)
	}
}

func TestEmbeddingDimensions_ModelTable(t *testing.T) {
	e := NewEmbedder("test-key", nil)
	cases := map[string]int{
		"text-embedding-3-small": 1536,
		"text-embedding-3-large": 3072,
		"text-embedding-ada-002": 1536,
		"unknown-model":          0,
	}
	for model, want := range cases {
		if got := e.EmbeddingDimensions(model); got != want {
			t.Errorf("EmbeddingDimensions(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestEmbed_ErrorTranslated(t *testing.T) {
	e := newTestEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_api_key","message":"bad key","type":"auth_error","param":""}}`))
	})
	_, err := e.Embed(context.Background(), "hi", "text-embedding-3-small")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("error not translated: %v", err)
	}
}
