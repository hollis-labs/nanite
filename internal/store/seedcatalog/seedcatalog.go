// Package seedcatalog holds compile-time seed values shipped with the binary.
//
// It deliberately carries NO default chat provider or model. Nanite has no
// user-level or app-level default: a provider and model are chosen per agent
// (definition/host settings) or per launch, and a blank selection is handled
// by the runtime that receives it (CW-20261011-0031).
package seedcatalog

// DefaultEmbeddingModelID is the seed value for an OpenAI embedding model
// used when no embedding model has been explicitly configured.
const DefaultEmbeddingModelID = "text-embedding-3-small"
