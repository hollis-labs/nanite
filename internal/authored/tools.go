package authored

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/elicitation"
	"gopkg.in/yaml.v3"
)

// Tool package kinds.
const (
	// ToolCommand runs the entry once per call: the call's JSON arguments on
	// standard input, a JSON result on standard output.
	ToolCommand = "command"
	// ToolMCP runs the entry as a stdio MCP server; its tools are offered as
	// tool__<package>__<tool>.
	ToolMCP = "mcp"
)

// Tool package limits.
const (
	// DefaultToolTimeout is a call's (or an MCP server request's) time limit
	// when tool.yaml sets none.
	DefaultToolTimeout = 60 * time.Second
	// MaxToolTimeout bounds tool.yaml's timeout, as Bash's is bounded.
	MaxToolTimeout = 10 * time.Minute
	// MaxToolNeeds bounds how many tokens one package may ask for.
	MaxToolNeeds = 8
)

// ToolPackage is an authored tool package, tools/<name>/tool.yaml: a tool an
// agent (or a person) wrote for the agent, run as a command per call or as a
// stdio MCP server. It is read again at the start of every turn.
type ToolPackage struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
	Kind        string `json:"kind"`
	// Entry is the executable, relative to tools/<name>/. It must be a
	// regular executable file inside the package (not a symlink).
	Entry string `json:"entry"`
	// InputSchema is the JSON Schema object for a command tool's arguments.
	// It defaults to an object with any properties.
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	// Timeout bounds one call of a command package (default
	// DefaultToolTimeout). An MCP package's calls use the MCP call limit.
	Timeout time.Duration `json:"timeout_ns,omitempty"`
	// Background runs each call as a background job: the call returns the
	// job at once and the job's finish arrives as a notice (command only).
	Background bool `json:"background,omitempty"`
	// Network declares that the tool reaches the network. It is shown to the
	// model and the approver; Nanite does not enforce it.
	Network bool `json:"network,omitempty"`
	// Env names host environment variables passed through to the tool. No
	// other host variable reaches it except Nanite's small base set (PATH,
	// HOME, USER, LANG, TERM, TMPDIR, SHELL, LC_*).
	Env []string `json:"env,omitempty"`
	// Needs are Warden tokens minted for the tool, each in its own variable.
	Needs []ToolNeed `json:"needs,omitempty"`
	// Digest is the SHA-256 of the whole package directory (every file's
	// path and content, and every symlink's target), set by LoadTools. An
	// approval is for one digest: any change to the package asks again.
	Digest string `json:"digest,omitempty"`
}

// ToolNeed asks for a short-lived Warden token for one audience and scope
// set, given to the tool in the variable Env. It needs identity.yaml's warden
// section.
type ToolNeed struct {
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes,omitempty"`
	Env      string   `json:"env"`
}

// Dir is the package's directory relative to the agent directory.
func (t ToolPackage) Dir() string { return "tools/" + t.Name }

var (
	toolName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	envName  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
)

// reservedEnv are variables a package may neither pass through nor fill
// with a token: Nanite sets them, or they change how processes run.
var reservedEnv = []string{"PATH", "HOME", "USER", "LANG", "TERM", "TMPDIR", "SHELL", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH"}

// CheckToolName reports why name cannot name a tool package: lower-case
// letters, digits and dashes, starting with a letter, at most 40 characters.
func CheckToolName(name string) error {
	if !toolName.MatchString(name) {
		return fmt.Errorf("invalid tool package name %q: use lower-case letters, digits and dashes, starting with a letter", name)
	}
	return nil
}

type toolFile struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version"`
	Kind        string         `yaml:"kind"`
	Entry       string         `yaml:"entry"`
	InputSchema map[string]any `yaml:"input_schema"`
	Timeout     string         `yaml:"timeout"`
	Background  bool           `yaml:"background"`
	Network     bool           `yaml:"network"`
	Env         []string       `yaml:"env"`
	Needs       []struct {
		Audience string   `yaml:"audience"`
		Scopes   []string `yaml:"scopes"`
		Env      string   `yaml:"env"`
	} `yaml:"needs"`
}

// ParseTool parses and validates tools/<name>/tool.yaml. It checks the
// manifest only; LoadTools also checks the entry file. allowEnv names
// credential-like host variables the host lets packages pass through (see
// CheckToolEnv).
func ParseTool(name string, data []byte, allowEnv ...string) (ToolPackage, error) {
	if err := CheckToolName(name); err != nil {
		return ToolPackage{}, err
	}
	var file toolFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return ToolPackage{}, fmt.Errorf("tool.yaml is empty")
		}
		return ToolPackage{}, fmt.Errorf("tool.yaml: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ToolPackage{}, fmt.Errorf("tool.yaml must contain one document")
	}
	if file.Name != "" && file.Name != name {
		return ToolPackage{}, fmt.Errorf("name %q does not match the directory %q", file.Name, name)
	}
	out := ToolPackage{Name: name, Description: strings.TrimSpace(file.Description), Version: strings.TrimSpace(file.Version),
		Kind: file.Kind, Entry: file.Entry, Background: file.Background, Network: file.Network, Timeout: DefaultToolTimeout}
	if out.Description == "" {
		return ToolPackage{}, fmt.Errorf("description is required")
	}
	switch out.Kind {
	case ToolCommand, ToolMCP:
	case "":
		return ToolPackage{}, fmt.Errorf("kind is required: %s or %s", ToolCommand, ToolMCP)
	default:
		return ToolPackage{}, fmt.Errorf("unknown kind %q: use %s or %s", out.Kind, ToolCommand, ToolMCP)
	}
	if err := checkEntry(out.Entry); err != nil {
		return ToolPackage{}, err
	}
	if file.Timeout != "" {
		d, err := time.ParseDuration(file.Timeout)
		if err != nil || d <= 0 {
			return ToolPackage{}, fmt.Errorf("timeout %q: use a positive duration such as 30s", file.Timeout)
		}
		if d > MaxToolTimeout {
			return ToolPackage{}, fmt.Errorf("timeout %s is above the %s limit", d, MaxToolTimeout)
		}
		out.Timeout = d
	}
	if out.Kind == ToolMCP {
		if file.Timeout != "" {
			return ToolPackage{}, fmt.Errorf("timeout is for kind command; an MCP server's calls use the MCP call limit")
		}
		out.Timeout = 0
		if file.InputSchema != nil {
			return ToolPackage{}, fmt.Errorf("input_schema is for kind command; an MCP server describes its own tools")
		}
		if out.Background {
			return ToolPackage{}, fmt.Errorf("background is for kind command")
		}
	} else {
		schema, err := toolSchema(file.InputSchema)
		if err != nil {
			return ToolPackage{}, err
		}
		out.InputSchema = schema
	}
	seen := map[string]string{}
	for _, name := range file.Env {
		if err := checkEnvName(name); err != nil {
			return ToolPackage{}, fmt.Errorf("env: %w", err)
		}
		if err := CheckToolEnv(name, allowEnv); err != nil {
			return ToolPackage{}, fmt.Errorf("env: %w", err)
		}
		if seen[name] != "" {
			return ToolPackage{}, fmt.Errorf("env: %s is listed twice", name)
		}
		seen[name] = "env"
		out.Env = append(out.Env, name)
	}
	if len(file.Needs) > MaxToolNeeds {
		return ToolPackage{}, fmt.Errorf("needs: at most %d tokens", MaxToolNeeds)
	}
	for i, need := range file.Needs {
		n := ToolNeed{Audience: strings.TrimSpace(need.Audience), Env: need.Env}
		if n.Audience == "" {
			return ToolPackage{}, fmt.Errorf("needs[%d]: audience is required", i)
		}
		for _, scope := range need.Scopes {
			scope = strings.TrimSpace(scope)
			if scope == "" || strings.ContainsAny(scope, " \t\n") {
				return ToolPackage{}, fmt.Errorf("needs[%d]: invalid scope %q", i, scope)
			}
			n.Scopes = append(n.Scopes, scope)
		}
		if err := checkEnvName(n.Env); err != nil {
			return ToolPackage{}, fmt.Errorf("needs[%d].env: %w", i, err)
		}
		if seen[n.Env] != "" {
			return ToolPackage{}, fmt.Errorf("needs[%d].env: %s is already used by %s", i, n.Env, seen[n.Env])
		}
		seen[n.Env] = fmt.Sprintf("needs[%d]", i)
		out.Needs = append(out.Needs, n)
	}
	return out, nil
}

func checkEntry(entry string) error {
	if entry == "" {
		return fmt.Errorf("entry is required")
	}
	clean := path.Clean(entry)
	if path.IsAbs(entry) || clean != entry || clean == "." || clean == "tool.yaml" || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(entry, "\\") {
		return fmt.Errorf("entry %q must be a clean relative path inside the package", entry)
	}
	return nil
}

func checkEnvName(name string) error {
	if !envName.MatchString(name) {
		return fmt.Errorf("invalid variable name %q: use upper-case letters, digits and underscores", name)
	}
	if slices.Contains(reservedEnv, name) || strings.HasPrefix(name, "LC_") || strings.HasPrefix(name, "NANITE_") {
		return fmt.Errorf("variable %s is reserved", name)
	}
	return nil
}

// toolSchema checks a command tool's input schema: a JSON Schema object of
// type object (the default when none is given).
func toolSchema(schema map[string]any) (json.RawMessage, error) {
	if schema == nil {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	if t, ok := schema["type"]; ok && t != "object" {
		return nil, fmt.Errorf("input_schema type must be object")
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("input_schema: %w", err)
	}
	// The same check an MCP tool's schema gets: a schema Nanite could not
	// safely validate calls against is refused.
	if _, err := elicitation.Resolve(raw); err != nil {
		return nil, fmt.Errorf("input_schema: %w", err)
	}
	return raw, nil
}

// providerKeyEnv are the variables Nanite itself reads provider keys from.
var providerKeyEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_CUSTOM_HEADERS"}

// credentialWords mark a variable name as a likely credential, the same
// words that mark a provider header as one.
var credentialWords = []string{"KEY", "TOKEN", "SECRET", "AUTH", "PASSWORD", "CREDENTIAL"}

// CheckToolEnv reports why a package may not pass the host variable name
// through: a provider key variable Nanite reads, or a name that looks like a
// credential (including database URLs, PATs, DSNs and password names),
// unless allow lists it. A package gets credentials as Warden tokens through
// needs instead.
func CheckToolEnv(name string, allow []string) error {
	if slices.Contains(allow, name) {
		return nil
	}
	if slices.Contains(providerKeyEnv, name) {
		return fmt.Errorf("%s holds a provider key Nanite uses; a package cannot read it", name)
	}
	if name == "DATABASE_URL" || name == "PAT" || name == "DSN" || strings.HasSuffix(name, "_PAT") || strings.HasSuffix(name, "_DSN") {
		return fmt.Errorf("%s looks like a credential; ask for a Warden token with needs, or have the host allow it", name)
	}
	for _, word := range credentialWords {
		if strings.Contains(name, word) {
			return fmt.Errorf("%s looks like a credential; ask for a Warden token with needs, or have the host allow it", name)
		}
	}
	return nil
}

// CheckToolEnvValue also refuses credentials embedded in a URL variable.
// Callers must validate the exact value they will pass, since the host
// environment can change after a manifest was parsed.
func CheckToolEnvValue(name, value string, allow []string) error {
	if err := CheckToolEnv(name, allow); err != nil {
		return err
	}
	if slices.Contains(allow, name) || name != "URL" && !strings.HasSuffix(name, "_URL") {
		return nil
	}
	endpoint, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL", name)
	}
	if endpoint.User != nil {
		return fmt.Errorf("%s contains URL credentials; have the host allow it explicitly", name)
	}
	for key := range endpoint.Query() {
		if CheckToolEnv(strings.ToUpper(key), nil) != nil {
			return fmt.Errorf("%s contains URL credentials; have the host allow it explicitly", name)
		}
	}
	return nil
}

// LoadTools reads the tool packages in dir/tools: each subdirectory with a
// tool.yaml. It returns every package that loaded and, joined, an error for
// each that did not. A missing tools directory is no packages. The entry
// must be a regular executable file inside the package.
func LoadTools(dir string, allowEnv ...string) (map[string]ToolPackage, error) {
	out := map[string]ToolPackage{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return out, fmt.Errorf("read tools: %w", err)
	}
	defer func() { _ = root.Close() }() // Read-only.
	entries, err := fs.ReadDir(root.FS(), "tools")
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("read tools: %w", err)
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		pkg, err := loadTool(root, name, allowEnv)
		if err != nil {
			errs = append(errs, fmt.Errorf("tools/%s: %w", name, err))
			continue
		}
		out[name] = pkg
	}
	return out, errors.Join(errs...)
}

// LoadTool reads one tool package, dir/tools/<name>, as LoadTools does.
func LoadTool(dir, name string, allowEnv ...string) (ToolPackage, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ToolPackage{}, fmt.Errorf("read tools: %w", err)
	}
	defer func() { _ = root.Close() }() // Read-only.
	info, err := root.Lstat("tools/" + name)
	if err != nil {
		return ToolPackage{}, err
	}
	if !info.IsDir() {
		return ToolPackage{}, fmt.Errorf("tools/%s is not a directory", name)
	}
	return loadTool(root, name, allowEnv)
}

func loadTool(root *os.Root, name string, allowEnv []string) (ToolPackage, error) {
	dir := "tools/" + name
	info, err := root.Lstat(dir + "/tool.yaml")
	if err != nil {
		return ToolPackage{}, fmt.Errorf("tool.yaml: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ToolPackage{}, fmt.Errorf("tool.yaml must be a regular file")
	}
	if info.Size() > 1<<20 {
		return ToolPackage{}, fmt.Errorf("tool.yaml is larger than 1 MiB")
	}
	data, err := readPackageFile(root, dir+"/tool.yaml", info, 1<<20)
	if err != nil {
		return ToolPackage{}, err
	}
	pkg, err := ParseTool(name, data, allowEnv...)
	if err != nil {
		return ToolPackage{}, err
	}
	for _, name := range pkg.Env {
		if value, ok := os.LookupEnv(name); ok {
			if err := CheckToolEnvValue(name, value, allowEnv); err != nil {
				return ToolPackage{}, err
			}
		}
	}
	entry, err := root.Lstat(dir + "/" + pkg.Entry)
	if err != nil {
		return ToolPackage{}, fmt.Errorf("entry %s: %w", pkg.Entry, err)
	}
	if !entry.Mode().IsRegular() {
		return ToolPackage{}, fmt.Errorf("entry %s must be a regular file, not a symlink or directory", pkg.Entry)
	}
	if entry.Mode().Perm()&0o100 == 0 {
		return ToolPackage{}, fmt.Errorf("entry %s is not executable (chmod +x)", pkg.Entry)
	}
	if st, ok := entry.Sys().(*syscall.Stat_t); ok && uint64(st.Nlink) > 1 { //nolint:unconvert // Nlink's width differs by platform.
		// A hard link could change the entry from outside the package
		// without changing anything Nanite reads under tools/.
		return ToolPackage{}, fmt.Errorf("entry %s has other hard links; copy it into the package instead", pkg.Entry)
	}
	if pkg.Digest, err = packageDigest(root, dir); err != nil {
		return ToolPackage{}, err
	}
	current, err := root.Lstat(dir + "/tool.yaml")
	if err != nil || versionOf(current) != versionOf(info) {
		return ToolPackage{}, fmt.Errorf("tool.yaml changed while loading the package")
	}
	return pkg, nil
}

// MaxToolPackageBytes bounds the files hashed for a package's digest.
const MaxToolPackageBytes = 64 << 20

// ToolNames returns the package names in sorted order.
func ToolNames(pkgs map[string]ToolPackage) []string {
	names := make([]string, 0, len(pkgs))
	for name := range pkgs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
