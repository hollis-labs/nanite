package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/mcpconfig"
	"github.com/hollis-labs/nanite/internal/store"
)

// MCPServerStore is the store surface MCPServerService reads and writes:
// the persisted MCP server configs.
type MCPServerStore interface {
	ListMCPServers(ctx context.Context) ([]store.MCPServerConfig, error)
	GetMCPServer(ctx context.Context, name string) (*store.MCPServerConfig, error)
	CreateMCPServer(ctx context.Context, cfg *store.MCPServerConfig) error
	UpdateMCPServer(ctx context.Context, cfg *store.MCPServerConfig) error
	DeleteMCPServer(ctx context.Context, name string) error
}

// MCPServerRegistrar is the part of the MCP manager that registers and
// removes a server's transport. *mcp.Manager satisfies it.
type MCPServerRegistrar interface {
	AddStdioServer(name, command string, args []string, env []string, envAllowlist []string, tier mcp.TrustTier) error
	AddRemoteServerFromConfig(name, transportType, url, headerJSON string, tier mcp.TrustTier) error
	RemoveServer(name string)
}

// MCPServerService owns the persisted MCP server configs and keeps the MCP
// manager in step with them: a write registers, re-registers or removes the
// server's transport and then runs tool discovery.
//
// Registrar and discover are optional; with neither, the service only
// writes rows. Registration and discovery failures are logged, never
// returned: the row write is what the caller asked for. Duplicate-check then
// create, and read then update, are separate steps, not one transaction.
//
// It also owns the header secrets rules (RedactHeaders, MergeRedactedHeaders),
// so every transport shows and accepts header values the same way.
type MCPServerService struct {
	store     MCPServerStore
	registrar MCPServerRegistrar
	discover  func(ctx context.Context) error
}

// NewMCPServerService builds the service. registrar and discover may be nil.
func NewMCPServerService(st MCPServerStore, registrar MCPServerRegistrar, discover func(ctx context.Context) error) *MCPServerService {
	return &MCPServerService{store: st, registrar: registrar, discover: discover}
}

// ErrMCPServerExists reports a create for a name that is already taken.
var ErrMCPServerExists = errors.New("server with this name already exists")

// MCPServerValidationError reports a config the rules reject. Its message is
// meant for the caller.
type MCPServerValidationError struct {
	Msg string
}

func (e *MCPServerValidationError) Error() string { return e.Msg }

// TransportTypeError is the message for an unrecognized transport_type. It
// names all three because "sse" and "streamable" are easy to pick wrongly:
// "sse" is the 2024-11-05 HTTP+SSE transport, "streamable" is JSON-RPC over
// POST — which is what a gateway-fronted /mcp URL speaks.
const TransportTypeError = "transport_type must be 'stdio', 'sse', or 'streamable'"

// ValidTransportType reports whether t is stdio, sse or streamable.
func ValidTransportType(t string) bool {
	switch t {
	case store.TransportStdio, store.TransportSSE, store.TransportStreamable:
		return true
	default:
		return false
	}
}

// List returns every persisted config, header values included. Transports
// redact before showing them.
func (s *MCPServerService) List(ctx context.Context) ([]store.MCPServerConfig, error) {
	return s.store.ListMCPServers(ctx)
}

// Get returns the named config, or nil when there is none.
func (s *MCPServerService) Get(ctx context.Context, name string) (*store.MCPServerConfig, error) {
	return s.store.GetMCPServer(ctx, name)
}

// Create validates, stores and registers a new server, then runs discovery.
// The name is required and an empty transport means stdio; a rule failure is
// a *MCPServerValidationError. A name already in use is ErrMCPServerExists;
// a failed duplicate lookup is not an error and the create goes ahead. The
// server is always created enabled. cfg is updated with what the store
// filled in (ID, defaults, timestamps). Store errors come back unwrapped.
func (s *MCPServerService) Create(ctx context.Context, cfg *store.MCPServerConfig) error {
	if cfg.Name == "" {
		return &MCPServerValidationError{Msg: "name is required"}
	}
	if cfg.TransportType == "" {
		cfg.TransportType = store.TransportStdio
	}
	if !ValidTransportType(cfg.TransportType) {
		return &MCPServerValidationError{Msg: TransportTypeError}
	}

	existing, _ := s.store.GetMCPServer(ctx, cfg.Name)
	if existing != nil {
		return ErrMCPServerExists
	}

	// Nothing is stored yet, so a placeholder has no value to keep: drop it
	// rather than store bullets.
	cfg.Headers = MergeRedactedHeaders(cfg.Headers, "")
	cfg.Env = MergeRedactedEnv(cfg.Env, "")

	cfg.Enabled = true
	if err := s.store.CreateMCPServer(ctx, cfg); err != nil {
		return err
	}

	s.register(cfg)
	if s.registrar != nil {
		s.runDiscovery("create", "server", cfg.Name)
	}
	return nil
}

// MCPServerPatch is an update to a stored MCP server config. A nil field is
// absent and keeps the stored value; a non-nil field replaces it, and an
// empty string there is an explicit clear (the store then applies its own
// default for trust_tier, env_allowlist and headers). Name, ID and the
// timestamps are not patchable.
type MCPServerPatch struct {
	TransportType *string
	Command       *string
	URL           *string
	Args          *string
	Env           *string
	Enabled       *bool
	TrustTier     *string
	EnvAllowlist  *string
	Headers       *string
}

// Update applies patch to the stored row existing, stores the result, then
// removes and re-registers the server's transport and runs discovery. It
// returns the stored row.
//
// The row is copied and then overwritten field by field, so every field the
// patch leaves nil — including any column added later — keeps its stored
// value. Headers that are present have their redaction placeholders restored
// from the stored values (MergeRedactedHeaders). A present but empty
// transport keeps the stored one; an unknown transport is a
// *MCPServerValidationError. Store errors come back unwrapped.
func (s *MCPServerService) Update(ctx context.Context, existing *store.MCPServerConfig, patch MCPServerPatch) (*store.MCPServerConfig, error) {
	row := *existing
	if patch.TransportType != nil && *patch.TransportType != "" {
		row.TransportType = *patch.TransportType
	}
	if patch.Command != nil {
		row.Command = *patch.Command
	}
	if patch.URL != nil {
		row.URL = *patch.URL
	}
	if patch.Args != nil {
		row.Args = *patch.Args
	}
	if patch.Env != nil {
		row.Env = MergeRedactedEnv(*patch.Env, existing.Env)
	}
	if patch.Enabled != nil {
		row.Enabled = *patch.Enabled
	}
	if patch.TrustTier != nil {
		row.TrustTier = *patch.TrustTier
	}
	if patch.EnvAllowlist != nil {
		row.EnvAllowlist = *patch.EnvAllowlist
	}
	if patch.Headers != nil {
		row.Headers = MergeRedactedHeaders(*patch.Headers, existing.Headers)
	}

	if !ValidTransportType(row.TransportType) {
		return nil, &MCPServerValidationError{Msg: TransportTypeError}
	}

	if err := s.store.UpdateMCPServer(ctx, &row); err != nil {
		return nil, err
	}

	if s.registrar != nil {
		s.registrar.RemoveServer(row.Name)
		s.register(&row)
		s.runDiscovery("update", "server", row.Name)
	}
	return &row, nil
}

// Delete removes the stored config and unregisters the server. The store's
// error comes back unwrapped.
func (s *MCPServerService) Delete(ctx context.Context, name string) error {
	if err := s.store.DeleteMCPServer(ctx, name); err != nil {
		return err
	}
	if s.registrar != nil {
		s.registrar.RemoveServer(name)
	}
	return nil
}

// Import creates a server for each entry of a .mcp.json payload that does
// not already exist by name, registers the created ones and, when any were
// created, runs discovery. Entries created before a failure stay created.
func (s *MCPServerService) Import(ctx context.Context, data []byte) (*mcpconfig.ImportResult, error) {
	result, err := mcpconfig.Import(ctx, s.store, data, DropRedactedPlaceholders)
	if err != nil {
		return nil, err
	}

	for _, name := range result.Created {
		cfg, _ := s.store.GetMCPServer(ctx, name)
		if cfg != nil {
			s.register(cfg)
		}
	}

	if s.registrar != nil && len(result.Created) > 0 {
		s.runDiscovery("import", "created", result.Created)
	}
	return result, nil
}

// Export returns every stored server as a .mcp.json config with every env
// value replaced by RedactedHeaderValue; header values are never exported
// (mcpconfig has no field for them). The result keeps each server's shape
// and env key names but cannot be re-imported to a working state. The
// complete file, secrets included, comes from mcpconfig.Export directly
// (`nanite mcp export`).
func (s *MCPServerService) Export(ctx context.Context) (*mcpconfig.ClaudeCodeConfig, error) {
	cfg, err := mcpconfig.Export(ctx, s.store)
	if err != nil {
		return nil, err
	}
	for name, entry := range cfg.MCPServers {
		for k := range entry.Env {
			entry.Env[k] = RedactedHeaderValue
		}
		cfg.MCPServers[name] = entry
	}
	return cfg, nil
}

// runDiscovery runs tool discovery after a write, on a fresh context so a
// finished request does not cut it short. A failure is logged.
func (s *MCPServerService) runDiscovery(after, key string, val any) {
	if s.discover == nil {
		return
	}
	if err := s.discover(context.Background()); err != nil {
		slog.Warn("mcpservers: MCP tool discovery after "+after+" failed", key, val, "err", err)
	}
}

// register registers the transport for an enabled server config.
func (s *MCPServerService) register(cfg *store.MCPServerConfig) {
	if s.registrar == nil || !cfg.Enabled {
		return
	}

	switch cfg.TransportType {
	case store.TransportStdio:
		args := decodeMCPStringSlice(cfg.Args, cfg.Name, "args")
		env := decodeMCPStringSlice(cfg.Env, cfg.Name, "env")
		envAllowlist := decodeMCPStringSlice(cfg.EnvAllowlist, cfg.Name, "env_allowlist")
		if err := s.registrar.AddStdioServer(cfg.Name, cfg.Command, args, env, envAllowlist, mcp.TrustTier(cfg.TrustTier)); err != nil {
			slog.Warn("mcpservers: failed to register stdio MCP server", "name", cfg.Name, "err", err)
		}
	default:
		if err := s.registrar.AddRemoteServerFromConfig(cfg.Name, cfg.TransportType, cfg.URL, cfg.Headers, mcp.TrustTier(cfg.TrustTier)); err != nil {
			// #nosec G706 -- name, transport, and err are structured operational diagnostics, not a formatted log message.
			slog.Warn("mcpservers: failed to register remote MCP server",
				"name", cfg.Name, "transport", cfg.TransportType, "err", err)
		}
	}
}

func decodeMCPStringSlice(raw, server, field string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		// #nosec G706 -- server, field, and err are structured operational diagnostics, not a formatted log message.
		slog.Warn("mcpservers: malformed MCP string-list json — ignoring", "server", server, "field", field, "err", err)
		return nil
	}
	return values
}

// RedactedHeaderValue replaces a stored header value on the way out.
//
// The key survives so a UI can show that an Authorization header exists
// without showing the token, and a client that sends this value back on an
// update is understood to mean "leave it alone" — see MergeRedactedHeaders.
const RedactedHeaderValue = "••••••••"

// RedactHeaders returns a config's headers JSON with every value replaced by
// RedactedHeaderValue. Headers hold credentials — a bearer token for a
// gateway, most often — so their values must not leave through a transport.
// Headers that are empty or do not parse come back as "{}": unparseable
// headers may still hold a secret.
func RedactHeaders(headersJSON string) string {
	parsed, err := mcp.ParseHeaderJSON(headersJSON)
	if err != nil || len(parsed) == 0 {
		return "{}"
	}
	masked := make(map[string]string, len(parsed))
	for k := range parsed {
		masked[k] = RedactedHeaderValue
	}
	encoded, err := json.Marshal(masked)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// MergeRedactedHeaders restores values the client sent back redacted.
//
// Without this the first save from a UI that loaded the list would overwrite a
// working token with a row of bullets, and the server would start returning
// 401s with nothing in the audit trail to explain why. A redacted value means
// "unchanged"; any other value, including an empty one, is a deliberate edit.
// A redacted value for a key that was never stored, or when the stored
// headers are empty or do not parse, is dropped rather than stored as
// bullets.
func MergeRedactedHeaders(incoming, stored string) string {
	in, err := mcp.ParseHeaderJSON(incoming)
	if err != nil || len(in) == 0 {
		return incoming
	}
	old, _ := mcp.ParseHeaderJSON(stored) // unparseable or empty: no stored values, so placeholders are dropped
	changed := false
	for k, v := range in {
		if v != RedactedHeaderValue {
			continue
		}
		if prev, ok := old[k]; ok {
			in[k] = prev
			changed = true
		} else {
			delete(in, k)
			changed = true
		}
	}
	if !changed {
		return incoming
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return incoming
	}
	return string(encoded)
}

// DropRedactedPlaceholders removes redaction placeholders from a config that
// has no stored values to restore them from, as on import, so bullets are
// never stored as a header or env value.
func DropRedactedPlaceholders(cfg *store.MCPServerConfig) {
	cfg.Headers = MergeRedactedHeaders(cfg.Headers, "")
	cfg.Env = MergeRedactedEnv(cfg.Env, "")
}

// RedactEnv returns a config's env JSON (an array of "KEY=VALUE" strings)
// with every value replaced by RedactedHeaderValue: "KEY=••••••••". Key names
// survive so a UI can show which variables are set; every value is hidden,
// since the server cannot tell a secret from DEBUG=1. An entry with no "="
// (ignored at runtime, but possibly a pasted bare token) becomes a bare
// placeholder. Order is kept. Env that does not parse comes back as "[]";
// env with no entries comes back unchanged.
func RedactEnv(envJSON string) string {
	if strings.TrimSpace(envJSON) == "" {
		return envJSON
	}
	var entries []string
	if err := json.Unmarshal([]byte(envJSON), &entries); err != nil {
		return "[]"
	}
	if len(entries) == 0 {
		return envJSON
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		if k, _, ok := strings.Cut(e, "="); ok {
			out[i] = k + "=" + RedactedHeaderValue
		} else {
			out[i] = RedactedHeaderValue
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// MergeRedactedEnv restores env values the client sent back redacted, the
// env counterpart of MergeRedactedHeaders. For each incoming entry, in order:
//   - "KEY=<placeholder>" with KEY stored: the stored "KEY=value" (the last
//     stored entry for KEY, matching the runtime's last-wins rule)
//   - "KEY=<placeholder>" with KEY not stored, or a bare placeholder: dropped
//   - anything else, including "KEY=": a deliberate edit, kept as sent
//
// Incoming env that does not parse is returned as sent. Stored env that is
// empty or does not parse has no values, so placeholders are dropped.
func MergeRedactedEnv(incoming, stored string) string {
	var in []string
	if err := json.Unmarshal([]byte(incoming), &in); err != nil {
		return incoming
	}
	var old []string
	_ = json.Unmarshal([]byte(stored), &old) // unparseable or empty: no stored values
	storedByKey := make(map[string]string, len(old))
	for _, e := range old {
		if k, _, ok := strings.Cut(e, "="); ok {
			storedByKey[k] = e
		}
	}

	out := make([]string, 0, len(in))
	changed := false
	for _, e := range in {
		if e == RedactedHeaderValue {
			changed = true
			continue
		}
		k, v, ok := strings.Cut(e, "=")
		if !ok || v != RedactedHeaderValue {
			out = append(out, e)
			continue
		}
		changed = true
		if prev, found := storedByKey[k]; found {
			out = append(out, prev)
		}
	}
	if !changed {
		return incoming
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return incoming
	}
	return string(encoded)
}
