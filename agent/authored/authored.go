// Package authored validates the Nanite-specific files an agent directory
// may hold beside the libs agentdir files: subtask variants
// (variants/<name>.md), providers.yaml, identity.yaml and tool packages
// (tools/<name>/tool.yaml). The engine loads them through
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
func ParseVariant(name string, data []byte) (Variant, error) {
	return authored.ParseVariant(name, data)
}

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
	AuthWarden  = authored.AuthWarden
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

// ToolPackage is an authored tool package, tools/<name>/tool.yaml: a tool an
// agent (or a person) wrote for the agent, run as a command per call or as a
// stdio MCP server.
type ToolPackage = authored.ToolPackage

// ToolNeed asks for a short-lived Warden token for a tool package.
type ToolNeed = authored.ToolNeed

// Tool package kinds and limits.
const (
	ToolCommand        = authored.ToolCommand
	ToolMCP            = authored.ToolMCP
	DefaultToolTimeout = authored.DefaultToolTimeout
	MaxToolTimeout     = authored.MaxToolTimeout
	MaxToolNeeds       = authored.MaxToolNeeds
)

// CheckToolName reports why name cannot name a tool package.
func CheckToolName(name string) error { return authored.CheckToolName(name) }

// ParseTool parses and validates the content of tools/<name>/tool.yaml. It
// checks the manifest only; LoadTools also checks the entry file.
// allowEnv names credential-like host variables the host lets packages pass
// through (see CheckToolEnv).
func ParseTool(name string, data []byte, allowEnv ...string) (ToolPackage, error) {
	return authored.ParseTool(name, data, allowEnv...)
}

// LoadTools reads the tool packages in dir/tools. It returns every package
// that loaded and, joined, an error for each that did not. A missing tools
// directory is no packages.
func LoadTools(dir string, allowEnv ...string) (map[string]ToolPackage, error) {
	return authored.LoadTools(dir, allowEnv...)
}

// LoadTool reads one tool package, dir/tools/<name>, as LoadTools does.
func LoadTool(dir, name string, allowEnv ...string) (ToolPackage, error) {
	return authored.LoadTool(dir, name, allowEnv...)
}

// CheckToolEnv reports why a package may not pass the host variable name
// through: a provider key variable Nanite reads, or a name that looks like a
// credential, unless allow lists it.
func CheckToolEnv(name string, allow []string) error { return authored.CheckToolEnv(name, allow) }

// Identity is a parsed identity.yaml: the agent's principal on the mesh,
// where it gets Warden tokens, and Warden auth for its MCP HTTP servers.
type Identity = authored.Identity

// WardenIdentity is identity.yaml's warden section: Warden's URL, the parent
// credential file (outside the agent directory) and a grant-request purpose.
type WardenIdentity = authored.WardenIdentity

// MCPIdentity is the Warden auth for one MCP HTTP server in .mcp.json.
type MCPIdentity = authored.MCPIdentity

// ParseIdentity parses and validates identity.yaml content. dir is the agent
// directory, which the credential file must be outside of.
func ParseIdentity(data []byte, dir string) (Identity, error) {
	return authored.ParseIdentity(data, dir)
}

// LoadIdentity reads and validates dir/identity.yaml. A missing file is the
// zero Identity, false and no error.
func LoadIdentity(dir string) (Identity, bool, error) { return authored.LoadIdentity(dir) }
