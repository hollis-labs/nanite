// Package seedcatalog is the compile-time source of provider/model seed
// defaults — the values shipped with the binary, used to populate the
// providers/models tables on first install and as the absolute routing
// floor for code that cannot ask the DB.
//
// SCOPE — known consumers (kept deliberately small):
//
//  1. internal/store/seed.go and store.SeedProviders — populate
//     providers.default_model and the primary anthropic provider row on
//     install / first boot.
//  2. internal/store/defaults.go — fills the provider hint when neither
//     the caller nor user_settings.default_provider supplies one, so the
//     per-provider default_model lookup has something to key on.
//  3. internal/chat/engine.go (InferProvider) — terminal routing floor
//     for unknown model strings.
//
// SCOPE — what does NOT belong here:
//
// "What default model/provider should this session use?" — that's an
// operator preference. It goes through store.ResolveProviderAndModel,
// which walks explicit args → user_settings → providers.default_model.
// Operators change those without a recompile; the seedcatalog values
// are the bootstrap floor below them.
//
// Note on enforcement: Go does not provide an import-graph mechanism to
// prevent a sub-package from being imported elsewhere. The "small list of
// known consumers" above is a convention, not a compile-time rule. If a
// new consumer appears, ask: "is this code looking for an operator-
// configurable default?" If yes, route it through the resolver instead.
//
// Background: CW-20260526-0003 (the bare-alias `claude-sonnet-4` 404)
// surfaced that pkg/models exported a `DefaultChatModelID` constant that
// every runtime fallback chain terminated on. Multiple chains converging
// on the same Go literal meant a stale value silently masked
// misconfiguration. The literals were physically relocated here to make
// the seed-vs-runtime distinction obvious by file location, and the
// runtime defaults were re-routed through the resolver.
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
