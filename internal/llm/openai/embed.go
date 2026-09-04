package openai

import (
	"context"
	"errors"
	"net/http"
	"os"

	embedcontracts "github.com/hollis-labs/go-embed-contracts"
	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// Embedder is the embedcontracts.Embedder implementation backed by openai-go.
//
// nanite picks Embedder construction at the composition root in
// internal/service/embedder_select.go; this type is the drop-in replacement
// for the deleted *provider.OpenAI returned for the "openai" provider id.
type Embedder struct {
	sdk    sdk.Client
	apiKey string
}

var _ embedcontracts.Embedder = (*Embedder)(nil)

// NewEmbedder constructs an Embedder. apiKey defaults to OPENAI_API_KEY when
// empty. httpClient may be nil; tests can pass a stub transport. Additional
// opts allow overriding the base URL for local/proxy testing.
func NewEmbedder(apiKey string, httpClient *http.Client, opts ...option.RequestOption) *Embedder {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	base := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	}
	if httpClient != nil {
		base = append(base, option.WithHTTPClient(httpClient))
	}
	base = append(base, opts...)
	return &Embedder{sdk: sdk.NewClient(base...), apiKey: apiKey}
}

// SetAPIKey rebuilds the SDK client with the new API key. Used by the
// provider registration path that resolves keys lazily after construction.
func (e *Embedder) SetAPIKey(key string) {
	e.apiKey = key
	e.sdk = sdk.NewClient(option.WithAPIKey(key), option.WithMaxRetries(0))
}

// Embed runs a single-input embedding request against the configured model.
func (e *Embedder) Embed(ctx context.Context, text, model string) (*embedcontracts.EmbeddingResult, error) {
	if model == "" {
		return nil, errors.New("openai embed: model is required")
	}
	resp, err := e.sdk.Embeddings.New(ctx, sdk.EmbeddingNewParams{
		Input: sdk.EmbeddingNewParamsInputUnion{OfString: sdk.String(text)},
		Model: sdk.EmbeddingModel(model),
	})
	if err != nil {
		return nil, translateError(err)
	}
	if len(resp.Data) == 0 {
		return nil, errors.New("openai embed: empty response")
	}
	return &embedcontracts.EmbeddingResult{
		Embedding:  toFloat32(resp.Data[0].Embedding),
		TokenCount: int(resp.Usage.TotalTokens),
	}, nil
}

// EmbedBatch runs a batch embedding request in a single API call. Per-row
// token counts are not surfaced by the OpenAI API, so total_tokens is split
// evenly across rows (matching Tesseract's wrapper convention).
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string, model string) ([]embedcontracts.EmbeddingResult, error) {
	if model == "" {
		return nil, errors.New("openai embed-batch: model is required")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	resp, err := e.sdk.Embeddings.New(ctx, sdk.EmbeddingNewParams{
		Input: sdk.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model: sdk.EmbeddingModel(model),
	})
	if err != nil {
		return nil, translateError(err)
	}
	out := make([]embedcontracts.EmbeddingResult, len(resp.Data))
	perRow := 0
	if len(resp.Data) > 0 {
		perRow = int(resp.Usage.TotalTokens) / len(resp.Data)
	}
	for i, em := range resp.Data {
		out[i] = embedcontracts.EmbeddingResult{
			Embedding:  toFloat32(em.Embedding),
			TokenCount: perRow,
		}
	}
	return out, nil
}

// EmbeddingDimensions returns the output vector size for known OpenAI
// embedding models. Returns 0 when the model is unknown — embedcontracts
// guarantees synchronous lookup with no I/O, and the API does not expose a
// dimensions endpoint.
//
// Decision (CW-20260508-0012, 2026-05-09): Option B — model→dim table — is
// strictly more useful than Tesseract's Option A (always 0) and matches
// the implementer prompt's recommendation. Nanite's
// pkg/models/registry.go.EmbeddingDimensionsFor is the broader portfolio
// authority; this table is a wrapper-local convenience that mirrors the
// three models nanite ships in its supported-providers default map.
func (e *Embedder) EmbeddingDimensions(model string) int {
	switch model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 0
	}
}

// toFloat32 converts the SDK's float64 embedding vector to nanite's float32
// representation. The truncation is acceptable: storage uses float32 too.
func toFloat32(vec []float64) []float32 {
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(v)
	}
	return out
}
