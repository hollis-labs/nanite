package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/store"
	"gopkg.in/yaml.v3"
)

type managedFileFrontmatter struct {
	ID                      string                `yaml:"id,omitempty"`
	Name                    string                `yaml:"name"`
	Slug                    string                `yaml:"slug"`
	Description             string                `yaml:"description,omitempty"`
	Icon                    string                `yaml:"icon,omitempty"`
	Avatar                  string                `yaml:"avatar,omitempty"`
	Tags                    []string              `yaml:"tags,omitempty"`
	Model                   string                `yaml:"model,omitempty"`
	Tools                   []string              `yaml:"tools,omitempty"`
	PermissionMode          string                `yaml:"permissionMode,omitempty"`
	MCPServers              []string              `yaml:"mcpServers,omitempty"`
	Directories             []string              `yaml:"directories,omitempty"`
	Constraints             map[string]any        `yaml:"constraints,omitempty"`
	ParentDispatchAllowlist []string              `yaml:"parentDispatchAllowlist,omitempty"`
	RoleTools               []string              `yaml:"roleTools,omitempty"`
	ContextPolicy           map[string]any        `yaml:"contextPolicy,omitempty"`
	Durable                 bool                  `yaml:"durable,omitempty"`
	ActivationMode          string                `yaml:"activationMode,omitempty"`
	Class                   string                `yaml:"class,omitempty"`
	DefaultState            string                `yaml:"defaultState,omitempty"`
	Procedures              []ProcedureDefinition `yaml:"procedures,omitempty"`
}

func EnsureManagedDirs(homeDir string) error {
	home := homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("agent: resolve home dir: %w", err)
		}
	}
	for _, dir := range []string{
		filepath.Join(home, ".nanite", "agents"),
		filepath.Join(home, ".nanite", "durable-agents"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("agent: ensure managed dir %s: %w", dir, err)
		}
	}
	return nil
}

func EnsureManagedConfigDirs(configRoot string) error {
	if strings.TrimSpace(configRoot) == "" {
		return fmt.Errorf("agent: managed config root is required")
	}
	for _, dir := range []string{
		filepath.Join(configRoot, "agents"),
		filepath.Join(configRoot, "durable-agents"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("agent: ensure managed dir %s: %w", dir, err)
		}
	}
	return nil
}

func UserManagedAgentPath(homeDir, slug string) (string, error) {
	home := homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("agent: resolve home dir: %w", err)
		}
	}
	return filepath.Join(home, ".nanite", "agents", slug+".md"), nil
}

// ManagedAgentPath resolves an agent slug to its managed-file path under
// configRoot's "agents" subdirectory. slug is validated against the
// canonical URL-safe slug pattern (ValidateSlug) before the join, and the
// join itself is confined via pathsafe.ResolveUnder as defense in depth —
// see GO-AGENT-001. Both guards run on every caller of this function.
func ManagedAgentPath(configRoot, slug string) (string, error) {
	if strings.TrimSpace(configRoot) == "" {
		return "", fmt.Errorf("agent: managed config root is required")
	}
	if strings.TrimSpace(slug) == "" {
		return "", fmt.Errorf("agent: slug is required")
	}
	if err := ValidateSlug(slug); err != nil {
		return "", fmt.Errorf("agent: %w", err)
	}
	agentsDir := filepath.Join(configRoot, "agents")
	resolved, err := pathsafe.ResolveUnder(agentsDir, slug+".md")
	if err != nil {
		return "", fmt.Errorf("agent: resolve managed path: %w", err)
	}
	return resolved, nil
}

func WriteManagedAgentProfile(path string, profile *store.AgentProfile, procedures []ProcedureDefinition) error {
	if profile == nil {
		return fmt.Errorf("profile is required")
	}
	fm := managedFileFrontmatter{
		ID:                      profile.ID,
		Name:                    profile.Name,
		Slug:                    profile.Slug,
		Description:             profile.Description,
		Icon:                    profile.Icon,
		Avatar:                  profile.Avatar,
		Tags:                    parseJSONArray(profile.Tags),
		Model:                   profile.DefaultModel,
		Tools:                   parseJSONArray(profile.Tools),
		MCPServers:              parseJSONArray(profile.MCPServers),
		Directories:             parseJSONArray(profile.Directories),
		Constraints:             parseJSONObject(profile.Constraints),
		ParentDispatchAllowlist: parseJSONArray(profile.ParentDispatchAllowlist),
		RoleTools:               parseJSONArray(profile.RoleTools),
		ContextPolicy:           parseJSONObject(profile.ContextPolicy),
		Durable:                 profile.Durable,
		ActivationMode:          profile.ActivationMode,
		Class:                   profile.Class,
		DefaultState:            profile.DefaultState,
		Procedures:              procedures,
	}
	if profile.CanExecute {
		fm.PermissionMode = "yolo"
	}
	raw, err := yaml.Marshal(&fm)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}
	body := strings.TrimSpace(profile.SystemPrompt)
	content := "---\n" + string(raw) + "---\n"
	if body != "" {
		content += body + "\n"
	}
	if err := atomicWriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write managed agent profile: %w", err)
	}
	return nil
}

// InjectFrontmatterID stamps `id: <id>` into an existing managed agent file's
// frontmatter without reserializing the rest of the document. Used by the
// boot reconcile pass to durably persist a minted/adopted UUID into a
// previously-unstamped writable file with minimal churn (it inserts a single
// line right after the opening `---`). It is a no-op if the file already
// declares an `id:`. The write is atomic (temp file + rename).
func InjectFrontmatterID(path, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("id is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	def, err := ParseMD(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(def.ID) != "" {
		return nil // already stamped
	}
	trimmed := bytesTrimLeftNewlines(raw)
	if !strings.HasPrefix(string(trimmed), "---") {
		return fmt.Errorf("%s: missing frontmatter delimiter", path)
	}
	// Insert the id line immediately after the opening delimiter line.
	s := string(trimmed)
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return fmt.Errorf("%s: malformed frontmatter", path)
	}
	updated := s[:nl+1] + "id: " + id + "\n" + s[nl+1:]
	return atomicWriteFile(path, []byte(updated), 0o644)
}

// FileRevision returns a stable content revision token (sha256 hex of the
// file bytes) used for optimistic-concurrency guards on managed agent edits.
// A missing file yields an empty token (no concurrency baseline).
func FileRevision(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return ContentRevision(raw), nil
}

// ContentRevision hashes raw bytes into the revision token form used by
// FileRevision.
func ContentRevision(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func bytesTrimLeftNewlines(b []byte) []byte {
	i := 0
	for i < len(b) && (b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return b[i:]
}

// atomicWriteFile writes data to a temp file in the same directory then
// renames it over the target, so readers never observe a partial write.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".agent-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName) // The rename removes this path on success; otherwise it is best-effort temp cleanup.
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // Preserve the write failure; close is cleanup for the abandoned temp file.
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close() // Preserve the chmod failure; close is cleanup for the abandoned temp file.
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}

func parseJSONArray(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseJSONObject(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
