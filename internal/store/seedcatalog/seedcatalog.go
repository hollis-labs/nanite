// Package seedcatalog is the compile-time source of provider/model seed
// defaults consumed by store.Seed and store.SeedProviders.
//
// SCOPE — strictly seed-only.
//
// Runtime "what default model should this session/agent use?" resolution
// MUST go through the resolver (internal/service/modeldefaults), which
// walks user_settings → providers.default_model. Operators change defaults
// via the DB without a recompile. Nothing in this package should be read
// from a runtime hot path.
//
// Background: CW-20260526-0003 (the bare-alias `claude-sonnet-4` 404)
// surfaced that pkg/models exported a `DefaultChatModelID` constant that
// the runtime fallback chains terminated on. Multiple chains, all dead-
// ending at the same Go literal, meant a stale value silently masked
// misconfiguration. The literals were physically relocated here so the
// import graph enforces the seed-only boundary: only internal/store/* may
// import seedcatalog, and runtime code that wants a default must call the
// resolver.
//
// If you find yourself importing this package from outside internal/store,
// you almost certainly want the resolver instead.
package seedcatalog

// DefaultChatModelID is the canonical wire-level model identifier seeded
// into providers.default_model for the Anthropic provider on first install
// (and any future install where the column is empty). It is NOT consulted
// by the chat-engine resolution chain at request time — see package doc.
const DefaultChatModelID = "claude-sonnet-4-20250514"

// DefaultProviderType is the seeded provider_type used when no other
// signal selects one. Used by:
//
//   - store.SeedProviders to identify the "primary" provider row whose
//     default_model gets DefaultChatModelID.
//   - chat.InferProvider as the routing-floor when an unknown model
//     string has no other resolution. This is a routing decision, not a
//     user-preference default — operators express user preferences via
//     user_settings.default_provider, which the resolver honors first.
const DefaultProviderType = "anthropic"

// DefaultEmbeddingModelID is the seed value for an OpenAI embedding model
// used when no embedding model has been explicitly configured.
const DefaultEmbeddingModelID = "text-embedding-3-small"

// ProviderDefaultModels maps provider_type → the wire-level model id used
// to populate providers.default_model when the row is first inserted or
// the column is empty. Add an entry here when seeding a new provider.
//
// Operators override per-install by updating providers.default_model
// directly; the seeder only fills empty values.
var ProviderDefaultModels = map[string]string{
	"anthropic": DefaultChatModelID,
	"openai":    "gpt-4o",
}
