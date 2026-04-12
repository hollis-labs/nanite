// Package provider registers direct dependencies on the four official provider
// SDKs so that go.mod keeps them in the require block even before every
// adapter has been swapped over.
//
// Phase 0.4 gates the swap of all provider adapters to the official SDKs:
//   - Anthropic       — anthropic-sdk-go (done, see anthropic.go)
//   - OpenAI          — openai-go        (pending, tasks 0.4.2)
//   - Ollama          — ollama/api       (pending, tasks 0.4.3)
//   - Google Gemini   — generative-ai-go (pending, tasks 0.4.4)
//
// The blank imports below are intentionally unused so that `go mod tidy` does
// not prune the SDKs between waves. Each parallel sub-agent that swaps its
// adapter over will drop the corresponding blank import when the real import
// lands in its adapter file.
package provider

import (
	// Placeholder blank imports — see package doc.
	_ "github.com/google/generative-ai-go/genai"
	_ "github.com/ollama/ollama/api"
	_ "github.com/openai/openai-go"
)
