package skillinstall

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/skill"
)

// ValidationError aggregates every problem found in a single Validate
// call, so a caller sees every issue at once rather than fixing one typo
// per install attempt.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("skillinstall: package validation failed (%d issue(s)): %s",
		len(e.Errors), strings.Join(e.Errors, "; "))
}

// DefaultValidator is the Validator this package's Installer uses when
// none is explicitly injected. It enforces:
//
//   - The real Agent-Skills-spec's mandatory frontmatter fields (name,
//     description) are present. ParsePackageDir has already rejected
//     anything that isn't even valid YAML/frontmatter-shaped by the time
//     Validate runs — this only covers fields that parse fine but are
//     empty.
//   - Context is exactly "inline" or "fork" (ParseMD already defaults an
//     empty value to "inline", so this only rejects a genuinely wrong
//     value, e.g. a typo).
//   - Every declared scripts:/references:/assets: entry names a real
//     path present in the package's file map — a "Scripts entry pointing
//     at a file that doesn't exist" package fails here with a specific,
//     per-entry message.
//   - Every declared parameter has a non-empty, unique name.
//   - Every declared dependency slug is non-empty, unique, and not a
//     self-reference. Real cycle/recursion-limit detection against the
//     graph of already-installed packages is TASKS/skills/07's job, not
//     this one — this is a cheap, single-package sanity check only.
type DefaultValidator struct{}

// Validate implements Validator.
func (DefaultValidator) Validate(def *skill.Definition, files skill.PackageFiles) error {
	var errs []string

	if def.Name == "" {
		errs = append(errs, "frontmatter: name is required")
	}
	if def.Description == "" {
		errs = append(errs, "frontmatter: description is required")
	}
	if def.Context != "inline" && def.Context != "fork" {
		errs = append(errs, fmt.Sprintf("frontmatter: context %q must be \"inline\" or \"fork\"", def.Context))
	}

	errs = append(errs, validateDeclaredFiles("scripts", def.Scripts, files)...)
	errs = append(errs, validateDeclaredFiles("references", def.References, files)...)
	errs = append(errs, validateDeclaredFiles("assets", def.Assets, files)...)

	errs = append(errs, validateParameters(def.Parameters)...)
	errs = append(errs, validateDependencies(def.Slug, def.Dependencies)...)

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

// validateDeclaredFiles confirms every path in declared (a
// scripts:/references:/assets: frontmatter list) normalizes to a
// well-formed, package-relative path and is actually present in files.
func validateDeclaredFiles(kind string, declared []string, files skill.PackageFiles) []string {
	var errs []string
	seen := make(map[string]bool, len(declared))
	for _, rel := range declared {
		clean := path.Clean(filepath.ToSlash(rel))
		if rel == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			errs = append(errs, fmt.Sprintf("%s: %q is not a valid package-relative path", kind, rel))
			continue
		}
		if seen[clean] {
			errs = append(errs, fmt.Sprintf("%s: %q declared more than once", kind, rel))
			continue
		}
		seen[clean] = true
		if _, ok := files[clean]; !ok {
			errs = append(errs, fmt.Sprintf("%s: %q does not exist in the package", kind, rel))
		}
	}
	return errs
}

// validateParameters confirms every declared parameter has a non-empty,
// unique name.
func validateParameters(params []skill.ParameterSpec) []string {
	var errs []string
	seen := make(map[string]bool, len(params))
	for i, p := range params {
		if p.Name == "" {
			errs = append(errs, fmt.Sprintf("parameters[%d]: name is required", i))
			continue
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Sprintf("parameters[%d]: duplicate parameter name %q", i, p.Name))
			continue
		}
		seen[p.Name] = true
	}
	return errs
}

// validateDependencies confirms every declared dependency slug is
// non-empty, unique, and not a self-reference. Real cycle detection
// against already-installed packages' own declared dependencies is
// TASKS/skills/07's job (internal/skillinstall's own "what to do" item 3
// names task 07's Installer-pipeline extension explicitly) — this check
// only catches a single package's own internally-inconsistent
// declaration.
func validateDependencies(slug string, deps []string) []string {
	var errs []string
	seen := make(map[string]bool, len(deps))
	for i, d := range deps {
		if d == "" {
			errs = append(errs, fmt.Sprintf("dependencies[%d]: empty dependency slug", i))
			continue
		}
		if d == slug {
			errs = append(errs, fmt.Sprintf("dependencies[%d]: skill %q cannot declare itself as a dependency", i, d))
		}
		if seen[d] {
			errs = append(errs, fmt.Sprintf("dependencies[%d]: duplicate dependency %q", i, d))
			continue
		}
		seen[d] = true
	}
	return errs
}
