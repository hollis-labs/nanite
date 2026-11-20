package authored

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/warden"
	"gopkg.in/yaml.v3"
)

// Identity is a parsed identity.yaml: who the agent is on the mesh and how
// it gets Warden tokens. It is Nanite's own file, beside host.yaml and
// .mcp.json (which other tools read too), and is read when the agent opens.
type Identity struct {
	// Principal is the agent's principal, a kind-prefixed id such as
	// agent:research-lead.
	Principal string `yaml:"principal" json:"principal"`
	// Warden configures token minting; nil means no Warden auth.
	Warden *WardenIdentity `yaml:"warden,omitempty" json:"warden,omitempty"`
	// MCP adds Warden auth and correlation headers to HTTP servers in
	// .mcp.json, by server name.
	MCP map[string]MCPIdentity `yaml:"mcp,omitempty" json:"mcp,omitempty"`
}

// WardenIdentity locates Warden and the agent's parent credential.
type WardenIdentity struct {
	// URL is Warden's base URL: https, or http for a loopback address.
	URL string `yaml:"url" json:"url"`
	// CredentialFile is the 0600 parent credential file: an absolute path,
	// or ~/ for the home directory. It must be outside the agent directory,
	// which is authored content and may be committed.
	CredentialFile string `yaml:"credential_file" json:"credential_file"`
	// Purpose explains grant requests Nanite files to the approver.
	Purpose string `yaml:"purpose,omitempty" json:"purpose,omitempty"`
}

// MCPIdentity is the Warden auth for one MCP HTTP server.
type MCPIdentity struct {
	// Audience and Scopes are the token's audience (mcp-gateway, say) and
	// scopes. An empty audience sends no token.
	Audience string   `yaml:"audience,omitempty" json:"audience,omitempty"`
	Scopes   []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	// Correlation sends the agent's principal and the conversation id as
	// claimed (unverified) headers: X-Nanite-Principal and
	// X-Nanite-Conversation.
	Correlation bool `yaml:"correlation,omitempty" json:"correlation,omitempty"`
}

var principalPattern = regexp.MustCompile(`^(agent|session|app|machine):[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

// ParseIdentity parses and validates identity.yaml content. Unknown fields
// are errors. dir is the agent directory, which the credential file must be
// outside of.
func ParseIdentity(data []byte, dir string) (Identity, error) {
	var id Identity
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&id); err != nil && !errors.Is(err, io.EOF) {
		return Identity{}, fmt.Errorf("identity.yaml: %w", err)
	}
	if err := id.Validate(dir); err != nil {
		return Identity{}, fmt.Errorf("identity.yaml: %w", err)
	}
	return id, nil
}

// Validate reports the first problem with id.
func (id Identity) Validate(dir string) error {
	if !principalPattern.MatchString(id.Principal) {
		return fmt.Errorf("principal %q must be kind:name with kind agent, session, app or machine", id.Principal)
	}
	if id.Warden != nil {
		if err := warden.ValidateURL(id.Warden.URL); err != nil {
			return err
		}
		path, err := ExpandHome(id.Warden.CredentialFile)
		if err != nil {
			return err
		}
		if dir != "" {
			if abs, err := filepath.Abs(dir); err == nil {
				if rel, err := filepath.Rel(abs, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return errors.New("warden credential_file must be outside the agent directory")
				}
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(id.MCP)) {
		m := id.MCP[name]
		if m.Audience == "" {
			if len(m.Scopes) > 0 {
				return fmt.Errorf("mcp %s: scopes need an audience", name)
			}
			continue
		}
		if id.Warden == nil {
			return fmt.Errorf("mcp %s: an audience needs the warden section", name)
		}
		if err := provider.ValidateAudience(m.Audience, m.Scopes); err != nil {
			return fmt.Errorf("mcp %s: %w", name, err)
		}
	}
	return nil
}

// ExpandHome resolves a credential path: absolute, or ~/ for the home
// directory.
func ExpandHome(path string) (string, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rest), nil
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("warden credential_file must be an absolute path or start with ~/")
	}
	return filepath.Clean(path), nil
}

// NewWardenSource returns the token source for id's warden section, or nil
// when id has none. Agent.Open and the public providers package both build
// it here, so they read identity.yaml the same way. It is a function, not a
// method, so the public Identity alias does not expose an internal type.
func NewWardenSource(id Identity, redactor *redact.Scope, logger *slog.Logger, onWaiting func(warden.Waiting)) (*warden.Source, error) {
	if id.Warden == nil {
		return nil, nil
	}
	file, err := ExpandHome(id.Warden.CredentialFile)
	if err != nil {
		return nil, err
	}
	return warden.New(warden.Config{URL: id.Warden.URL, CredentialFile: file, Principal: id.Principal, Purpose: id.Warden.Purpose,
		Redactor: redactor, Logger: logger, OnWaiting: onWaiting})
}

// LoadIdentity reads and validates dir/identity.yaml through a root, so a
// symlink cannot reach outside the directory. A missing file is the zero
// Identity, false and no error.
func LoadIdentity(dir string) (Identity, bool, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Identity{}, false, err
	}
	defer func() { _ = root.Close() }() // Read-only.
	raw, err := root.ReadFile("identity.yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, fmt.Errorf("identity.yaml: %w", err)
	}
	id, err := ParseIdentity(raw, dir)
	return id, err == nil, err
}
