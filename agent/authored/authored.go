// Package authored validates the Nanite-specific files an agent directory
// may hold beside the libs agentdir files: subtask variants
// (variants/<name>.md) and providers.yaml. The engine loads them through
// the same rules, so a tool that edits an agent directory (an editor, a
// workbench, CI) applies exactly the rules Nanite will.
//
// The parse functions take bytes, for content not yet written; the load
// functions read an agent directory through an os.Root, so a symlink cannot
// reach outside it. None of them resolve keys, contact a provider or start
// anything.
package authored

import "github.com/hollis-labs/nanite/internal/authored"

// MaxLastN bounds a variant's last_n and a subtask call's context.last_n.
const MaxLastN = authored.MaxLastN

// Variant is an authored variant, variants/<name>.md: a role prompt (the
// body), the tools it may use, an optional provider and model, and budgets.
type Variant = authored.Variant

// Budgets bound one subtask. Zero means the agent's own limit applies.
type Budgets = authored.Budgets

// BuiltinVariants names the variants every agent has, sorted. An authored
// variant cannot take one of these names.
func BuiltinVariants() []string { return authored.BuiltinVariants() }

// CheckVariantName reports why name cannot name an authored variant: it must
// be lower-case letters, digits and dashes, start with a letter, be at most
// 40 characters, and be neither "chat" nor a built-in variant.
func CheckVariantName(name string) error { return authored.CheckVariantName(name) }

// ParseVariant parses and validates the content of variants/<name>.md.
func ParseVariant(name string, data []byte) (Variant, error) { return authored.ParseVariant(name, data) }

// LoadVariants reads the authored variants in dir/variants (*.md; other
// files are ignored). It returns every variant that loaded and, joined, an
// error for each file that did not. A missing variants directory is no
// variants.
func LoadVariants(dir string) (map[string]Variant, error) { return authored.LoadVariants(dir) }

// ProviderSpec is a named provider in providers.yaml (the same type as
// agent.ProviderSpec): an endpoint, its wire and a key reference, never a
// key. ProviderSpec.Validate checks one.
type ProviderSpec = authored.ProviderSpec

// Selection names a provider (built in or named) and a model.
type Selection = authored.Selection

// Providers is a parsed providers.yaml: the named providers, each with Name
// set, and the selection per use.
type Providers = authored.Providers

// Uses a provider can be selected for. A use not listed falls back to chat;
// chat not listed uses host.yaml's runtime.provider and runtime.model.
const (
	UseChat       = authored.UseChat
	UseSummarizer = authored.UseSummarizer
	UseSubtask    = authored.UseSubtask
	UseDesk       = authored.UseDesk
)

// Uses lists every use.
func Uses() []string { return authored.Uses() }

// Wires a named provider can speak (ProviderSpec.Wire).
const (
	WireAnthropic       = authored.WireAnthropic
	WireOpenAI          = authored.WireOpenAI
	WireOpenAIChat      = authored.WireOpenAIChat
	WireOpenAIResponses = authored.WireOpenAIResponses
)

// Auth schemes for a named provider (ProviderSpec.Auth).
const (
	AuthDefault = authored.AuthDefault
	AuthBearer  = authored.AuthBearer
	AuthNone    = authored.AuthNone
)

// BuiltinProviders names the built-in providers, which providers.yaml can
// select but not redefine.
func BuiltinProviders() []string { return authored.BuiltinProviders() }

// ParseProviders parses and validates providers.yaml content. Unknown fields
// are errors. A use may name a provider not defined here, since the host may
// supply it.
func ParseProviders(data []byte) (Providers, error) { return authored.ParseProviders(data) }

// LoadProviders reads and validates dir/providers.yaml. A missing file is
// the zero Providers and no error.
func LoadProviders(dir string) (Providers, error) { return authored.LoadProviders(dir) }
