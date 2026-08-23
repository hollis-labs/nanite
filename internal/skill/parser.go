package skill

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/pathsafe"
	"gopkg.in/yaml.v3"
)

// Definition is the typed representation of a skill MD file.
// YAML frontmatter fields map to struct tags; the markdown body
// after the closing --- becomes the Prompt.
type Definition struct {
	// Identity
	Name        string   `yaml:"name"`
	Slug        string   `yaml:"slug"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`

	// Behavior
	ArgumentHint string   `yaml:"argument-hint"` // hint text for argument input
	AllowedTools []string `yaml:"allowed-tools"` // tool allowlist during execution
	Model        string   `yaml:"model"`         // preferred model override
	Effort       string   `yaml:"effort"`        // low, medium, high
	Context      string   `yaml:"context"`       // "inline" (default) or "fork"
	// E2 (CW-20260428-0017): mode binding. Slugs of modes the skill is bound to.
	// Empty / missing = available in every mode (back-compat). Slugs are
	// resolved → mode IDs at ingest time by service/ingest.go.
	Modes []string `yaml:"modes"`

	// Parameters declares the skill's invocation-time parameters —
	// TASKS/skills/06's Skill Resolver binds against this shape when
	// resolving a caller's static args and/or an agent_context_resolvers
	// dynamic binding (see ParameterSpec's own doc comment). Optional — a
	// skill with no declared parameters takes none.
	Parameters []ParameterSpec `yaml:"parameters"`

	// Scripts, References, Assets declare the package's scripts/,
	// references/, assets/ subdirectory entries, by path relative to the
	// package root (e.g. "scripts/run.sh"). These are declarative — the
	// actual bytes always come from what ParsePackageDir finds on disk
	// under the package root, never from these lists directly. Install-
	// time validation (internal/skillinstall's Validator step,
	// TASKS/skills/04) confirms every declared entry here actually exists
	// among the package's files; a scripts:/references:/assets: entry
	// naming a file that isn't present is a validation failure, not a
	// silent skip.
	Scripts    []string `yaml:"scripts"`
	References []string `yaml:"references"`
	Assets     []string `yaml:"assets"`

	// Dependencies declares the slugs of other skills this package
	// composes as nested dependencies. TASKS/skills/07 gives inline/fork
	// composition real semantics at materialization time and adds
	// install-time cycle/recursion-limit detection against the graph this
	// field feeds; this parser's own job (TASKS/skills/04) is limited to
	// recognizing the raw declaration and handing the slug list through to
	// install-time indexing (store.Skill.DeclaredDependencies). No
	// established Agent-Skills-spec convention exists for this key yet —
	// "dependencies" is this batch's own choice, not a spec requirement.
	Dependencies []string `yaml:"dependencies"`

	// Prompt is the markdown body below the YAML frontmatter.
	// May contain !`command` dynamic context markers.
	Prompt string `yaml:"-"`

	// Metadata set by the loader, not parsed from file.
	Source    string `yaml:"-"` // "builtin", "project", "user", "plugin", "nanite", "claude"
	SourceRef string `yaml:"-"` // file path or "embedded:*.md" or a package directory path
}

// ParameterSpec declares one parameter a skill's SKILL.md frontmatter
// exposes to callers. Name/Description/Required describe the parameter
// itself; ResolverSlot optionally names an existing
// agent_context_resolvers row (by its slot_name column,
// internal/runtime/agent/context_resolver.go) whose dynamically-resolved
// value supplies this parameter when the caller doesn't pass a static
// argument. TASKS/skills/06's Skill Resolver is the sole consumer of
// ResolverSlot — this package only parses and validates the declaration,
// it does not resolve anything itself.
type ParameterSpec struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description,omitempty"`
	Required     bool   `yaml:"required,omitempty"`
	ResolverSlot string `yaml:"resolver_slot,omitempty"`
}

var frontmatterDelim = []byte("---")

// ParseMD parses a markdown file with YAML frontmatter into a Definition.
func ParseMD(data []byte) (*Definition, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	var def Definition
	if err := yaml.Unmarshal(fm, &def); err != nil {
		return nil, fmt.Errorf("skill: invalid YAML frontmatter: %w", err)
	}

	// Slug is validated in ParseMDFile after filename fallback.
	// ParseMD callers that don't use ParseMDFile must ensure slug is set.

	def.Prompt = strings.TrimSpace(string(body))

	// Default context mode is inline.
	if def.Context == "" {
		def.Context = "inline"
	}

	return &def, nil
}

// ParseMDFile reads a file from disk and parses it as a skill definition.
// If the parsed definition has no slug, the filename (without extension) is used.
func ParseMDFile(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: read %s: %w", path, err)
	}

	def, err := ParseMD(data)
	if err != nil {
		return nil, fmt.Errorf("skill: parse %s: %w", path, err)
	}

	// Fall back to filename-derived slug when frontmatter omits it.
	if def.Slug == "" {
		def.Slug = SlugFromFilename(path)
	}
	if def.Slug == "" {
		return nil, fmt.Errorf("skill: %s has no slug in frontmatter and none could be derived from filename", path)
	}

	def.SourceRef = path
	return def, nil
}

// skillFileName is the required package-root filename for a skill's
// frontmatter+body content, matching the real Agent-Skills-spec shape.
const skillFileName = "SKILL.md"

// PackageFiles is the in-memory representation of every regular file in a
// skill package, keyed by its path relative to the package root
// (forward-slash separated, e.g. "SKILL.md", "scripts/run.sh"). This is
// structurally identical to internal/skillvendor.FileMap (both
// map[string][]byte) — TASKS/skills/03's vendored store is the intended
// destination for this map, via an explicit type conversion at the call
// site in internal/skillinstall. This package deliberately does not
// import internal/skillvendor for a plain map-shape alias.
type PackageFiles map[string][]byte

// ParsePackageDir reads a full skill package rooted at dir: a required
// SKILL.md file at the package root (parsed exactly as ParseMD/ParseMDFile
// already do), plus every other regular file under dir — anything under
// scripts/, references/, assets/, or elsewhere in the package root.
//
// The returned Definition's slug falls back to the package directory's own
// base name (matching ParseMDFile's filename-fallback behavior) when the
// frontmatter omits one. SourceRef is set to dir.
//
// ParsePackageDir does not itself enforce that a declared
// Scripts/References/Assets/Dependencies entry corresponds to real,
// well-formed content — confirming the real Agent-Skills-spec shape is
// internal/skillinstall's Validator step's job (TASKS/skills/04), a
// distinct install-time concern from parsing. A package that fails that
// later validation still parses successfully here: this function's job is
// only "here is what's declared, and here is what's actually on disk,"
// not judging whether the two agree.
func ParsePackageDir(dir string) (*Definition, PackageFiles, error) {
	skillPath, err := pathsafe.ResolveUnder(dir, skillFileName)
	if err != nil {
		return nil, nil, fmt.Errorf("skill: resolve %s under package %s: %w", skillFileName, dir, err)
	}
	// #nosec G304 -- skillPath is confined to the selected package root above.
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, nil, fmt.Errorf("skill: read %s: %w", skillPath, err)
	}

	def, err := ParseMD(data)
	if err != nil {
		return nil, nil, fmt.Errorf("skill: parse %s: %w", skillPath, err)
	}

	if def.Slug == "" {
		def.Slug = SlugFromFilename(dir)
	}
	if def.Slug == "" {
		return nil, nil, fmt.Errorf("skill: package %s has no slug in frontmatter and none could be derived from the package directory name", dir)
	}
	def.SourceRef = dir

	files, err := readPackageTree(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("skill: read package tree %s: %w", dir, err)
	}

	return def, files, nil
}

// readPackageTree walks dir and returns every regular file's content as a
// PackageFiles map keyed by dir-relative, forward-slash path.
func readPackageTree(dir string) (PackageFiles, error) {
	files := PackageFiles{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		content, readErr := readPackageFile(dir, rel)
		if readErr != nil {
			return readErr
		}
		files[filepath.ToSlash(rel)] = content
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("skill: package directory %s contains no files", dir)
	}
	return files, nil
}

// readPackageFile confines each walked path to the caller-selected package
// root before reading it. The package root itself is an explicit local-install
// capability; symlinks and traversal beneath that root must not widen it.
func readPackageFile(root, rel string) ([]byte, error) {
	resolved, err := pathsafe.ResolveUnder(root, rel)
	if err != nil {
		return nil, fmt.Errorf("skill: resolve package file %q: %w", rel, err)
	}
	// #nosec G304 -- resolved is confined to root by ResolveUnder above.
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("skill: read package file %q: %w", rel, err)
	}
	return content, nil
}

// SlugFromFilename derives a slug from a markdown filename.
func SlugFromFilename(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// splitFrontmatter splits data into YAML frontmatter and markdown body.
func splitFrontmatter(data []byte) (frontmatter, body []byte, err error) {
	data = bytes.TrimLeft(data, "\n\r")

	if !bytes.HasPrefix(data, frontmatterDelim) {
		return nil, nil, fmt.Errorf("skill: file does not start with --- frontmatter delimiter")
	}

	rest := data[len(frontmatterDelim):]
	if _, after, ok := bytes.Cut(rest, []byte("\n")); ok {
		rest = after
	} else {
		return nil, nil, fmt.Errorf("skill: no content after opening --- delimiter")
	}

	before, after, found := bytes.Cut(rest, append([]byte("\n"), frontmatterDelim...))
	if !found {
		return nil, nil, fmt.Errorf("skill: missing closing --- delimiter")
	}

	frontmatter = before

	if _, bodyPart, ok := bytes.Cut(after, []byte("\n")); ok {
		body = bodyPart
	}

	return frontmatter, body, nil
}
