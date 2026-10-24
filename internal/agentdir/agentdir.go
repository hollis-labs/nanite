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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// HarnessProfile and MCPServers are host knobs the stored host settings
	// already carry. A cli host refuses them where its runtime cannot honor them
	// (see service.AdmitCLIHost); the loader passes them through unjudged.
	HarnessProfile string   `yaml:"harness_profile"`
	MCPServers     []string `yaml:"mcp_servers"`
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
	// Skills names the skill folders present. Those agent.md requests are packed
	// into Resources (the pinned tree); the rest are reported and left alone.
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
	// host.yaml is one document: anything after it would be silently ignored.
	var extra yaml.Node
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("more than one YAML document")
		}
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
	for _, sk := range d.Requirements.Skills {
		res, err := packSkill(root, sk)
		if err != nil {
			return nil, err
		}
		if seen[res.URI] {
			return nil, fmt.Errorf("skill %q is requested twice", sk.Name)
		}
		seen[res.URI] = true
		out = append(out, res)
	}
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

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// packSkill reads skills/<name>/ and returns the canonical tree as the resource
// the skill's pin must match. The pin covers every file in the folder.
func packSkill(root string, sk agentdef.Skill) (Resource, error) {
	if !skillNamePattern.MatchString(sk.Name) {
		return Resource{}, fmt.Errorf("skill %q: use lowercase letters, digits and single hyphens", sk.Name)
	}
	want := "skills/" + sk.Name
	if strings.TrimPrefix(sk.Content.URI, "file:") != want {
		return Resource{}, fmt.Errorf("skill %q: content uri must be %q, got %q", sk.Name, "file:"+want, sk.Content.URI)
	}
	dir := filepath.Join(root, "skills", sk.Name)
	files := map[string][]byte{}
	total := 0
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.IsDir() {
			return nil
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file; a skill folder holds files and directories only", p)
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		body, readErr := readFile(root, filepath.Join("skills", sk.Name, rel))
		if readErr != nil {
			return readErr
		}
		if total += len(body); total > agentpolicy.MaxSkillTreeBytes {
			return fmt.Errorf("skill folder is larger than %d bytes", agentpolicy.MaxSkillTreeBytes)
		}
		files[filepath.ToSlash(rel)] = body
		return nil
	})
	if err != nil {
		return Resource{}, fmt.Errorf("skill %q: %w", sk.Name, err)
	}
	blob, err := agentpolicy.EncodeSkillTree(files)
	if err != nil {
		return Resource{}, fmt.Errorf("skill %q: %w", sk.Name, err)
	}
	if got := agentdef.ArtifactDigest(blob); got != sk.Content.Digest {
		return Resource{}, fmt.Errorf("skill %q: content.digest is %s but the folder packs to %s; update agent.md", sk.Name, sk.Content.Digest, got)
	}
	return Resource{URI: sk.Content.URI, Content: blob}, nil
}

// CheckOwnDirectories refuses an agent directory that is inside, or contains, a
// directory its own host.yaml lets the agent use. An agent that can write its own
// agent.md could otherwise hand new text to the next reload. Paths are compared
// after resolving symlinks in their existing part.
func CheckOwnDirectories(agentDir string, directories []string) error {
	root := resolveLoose(agentDir)
	for _, dir := range directories {
		d := resolveLoose(dir)
		if within(d, root) || within(root, d) {
			return fmt.Errorf("the agent directory %s overlaps %s, a directory host.yaml grants the agent: it could rewrite its own definition", agentDir, dir)
		}
	}
	return nil
}

// resolveLoose evaluates symlinks in the deepest existing ancestor of p, so a
// directory that does not exist yet still compares by where it would be.
func resolveLoose(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}
