// Package agentdir reads an agent directory: the authored source of an agent.
//
//	<agent>/
//	  agent.md                agentdef v2: behavior, requirements, harness policy, continuity
//	  host.yaml               where and how it runs: runtime, provider, model, directories
//	  skills/<name>/SKILL.md  one folder per skill (listed, not yet installable)
//	  context/...             files agent.md pins as resources
//
// It only reads and validates. Writing to the database is the caller's job, so
// reading an edited directory has no effect until an operator loads it.
package agentdir

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"gopkg.in/yaml.v3"
)

const (
	DefinitionFile = "agent.md"
	HostFile       = "host.yaml"
	maxFile        = 1 << 20
)

// Mode says how edits to the directory reach new sessions.
type Mode string

const (
	// ModeUnset leaves the choice to the operator's invocation.
	ModeUnset Mode = ""
	// ModeLive publishes a new revision on every load.
	ModeLive Mode = "live"
	// ModePinned refuses to publish another revision unless forced.
	ModePinned Mode = "pinned"
)

// Host is the parsed host.yaml. Fields are those of the stored host settings
// document, plus the slug, title and mode of the loaded agent.
type Host struct {
	Version     string   `yaml:"version"`
	Slug        string   `yaml:"slug"`
	Title       string   `yaml:"title"`
	Mode        Mode     `yaml:"mode"`
	Runtime     string   `yaml:"runtime"`
	Provider    string   `yaml:"provider"`
	Model       string   `yaml:"model"`
	Directories []string `yaml:"directories"`
}

// Resource is a file the definition pins.
type Resource struct {
	URI     string
	Content []byte
}

// Directory is a validated agent directory.
type Directory struct {
	Path       string
	Definition []byte
	Parsed     *agentdef.Definition
	Host       Host
	Resources  []Resource
	// Skills names the skill folders present. They are reported, not installed.
	Skills []string
}

// Discover finds the agent directory for arg: arg itself when it holds an
// agent.md, else arg/.nanite. An empty arg means the current directory.
func Discover(arg string) (string, error) {
	if arg == "" {
		arg = "."
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	for _, candidate := range []string{abs, filepath.Join(abs, ".nanite")} {
		if fi, statErr := os.Stat(filepath.Join(candidate, DefinitionFile)); statErr == nil && fi.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no %s in %s or %s", DefinitionFile, abs, filepath.Join(abs, ".nanite"))
}

// Load reads and validates the directory at path.
func Load(path string) (*Directory, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	d := &Directory{Path: root}
	if d.Definition, err = readFile(root, DefinitionFile); err != nil {
		return nil, err
	}
	if d.Parsed, err = agentdef.Parse(d.Definition, agentpolicy.Option()); err != nil {
		return nil, fmt.Errorf("%s is not a valid definition: %w", DefinitionFile, err)
	}
	raw, err := readFile(root, HostFile)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err = dec.Decode(&d.Host); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", HostFile, err)
	}
	if err = d.Host.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", HostFile, err)
	}
	if d.Resources, err = readResources(root, d.Parsed); err != nil {
		return nil, err
	}
	d.Skills = listSkills(root)
	return d, nil
}

func (h *Host) check() error {
	if h.Version == "" {
		h.Version = "1"
	}
	if h.Version != "1" {
		return fmt.Errorf("version %q is not supported; use \"1\"", h.Version)
	}
	switch h.Runtime {
	case "api", "cli":
	default:
		return fmt.Errorf("runtime %q is not supported; use api or cli", h.Runtime)
	}
	switch h.Mode {
	case ModeUnset, ModeLive, ModePinned:
	default:
		return fmt.Errorf("mode %q is not supported; use live or pinned", h.Mode)
	}
	for _, dir := range h.Directories {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("directory %q must be an absolute path", dir)
		}
	}
	if h.Runtime == "cli" && len(h.Directories) == 0 {
		return errors.New("directories is required for runtime cli: the first entry is the work root")
	}
	return nil
}

// readFile reads a regular file inside root, refusing a symlink out of it.
func readFile(root, name string) ([]byte, error) {
	p := filepath.Join(root, name)
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", name)
	}
	if resolved, evalErr := filepath.EvalSymlinks(p); evalErr != nil || !within(root, resolved) {
		return nil, fmt.Errorf("%s resolves outside the agent directory", name)
	}
	if fi.Size() > maxFile {
		return nil, fmt.Errorf("%s exceeds 1 MiB", name)
	}
	return os.ReadFile(p) // #nosec G304 -- p is a regular file directly inside the directory the operator named
}

func within(root, p string) bool {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// readResources resolves every pinned reference to a file inside the directory.
// A reference is a relative path, optionally written file:<path>.
func readResources(root string, d *agentdef.Definition) ([]Resource, error) {
	refs := append([]agentdef.Ref{}, d.Behavior.Instructions...)
	refs = append(refs, d.Behavior.SOPs...)
	refs = append(refs, d.Requirements.Resources...)
	refs = append(refs, d.HarnessProfile.Steering...)
	refs = append(refs, d.HarnessProfile.Context.Sources...)
	refs = append(refs, d.HarnessProfile.Approvals...)
	refs = append(refs, d.HarnessProfile.Escalation...)
	for _, r := range []*agentdef.Ref{d.HarnessProfile.Context.Policy, d.Continuity.MemoryPolicy, d.Continuity.RecoveryStrategy} {
		if r != nil {
			refs = append(refs, *r)
		}
	}
	seen := map[string]bool{}
	var out []Resource
	for _, r := range refs {
		if seen[r.URI] {
			continue
		}
		seen[r.URI] = true
		rel := strings.TrimPrefix(r.URI, "file:")
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(clean, ":") {
			return nil, fmt.Errorf("resource %q is not a file inside the agent directory", r.URI)
		}
		body, err := readFile(root, clean)
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", r.URI, err)
		}
		out = append(out, Resource{URI: r.URI, Content: body})
	}
	return out, nil
}

func listSkills(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
