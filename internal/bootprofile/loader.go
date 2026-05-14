package bootprofile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Subdirectory names the catalog layout expects under the root. Kept as
// constants so a future ticket can introduce a CatalogLayout struct if
// the layout ever needs to be configurable per repo.
const (
	profilesSubdir = "boot-profiles"
	launchesSubdir = "launches"
)

// ErrProfileNotFound is returned when Compile (or any future lookup
// helper) cannot find the requested profile ID. Callers should match
// against it with errors.Is so the surrounding message can be improved
// without breaking callers.
var ErrProfileNotFound = errors.New("bootprofile: profile not found")

// ErrLaunchNotFound is returned when Compile cannot find the requested
// launch ID. Same use pattern as ErrProfileNotFound.
var ErrLaunchNotFound = errors.New("bootprofile: launch not found")

// LoadCatalog reads boot profiles and launches from the given catalog
// root, returning a populated Catalog. A missing root directory is
// NOT an error — the function returns an empty catalog so callers can
// safely invoke it from cmd/nanite/main.go startup without checking
// "is the catalog configured?" first.
//
// File scan rules:
//
//   - <root>/boot-profiles/*.yaml — each parsed as a Profile.
//   - <root>/launches/*.yaml      — each parsed as a Launch.
//   - subdirectories are skipped.
//   - files other than .yaml are skipped.
//
// Errors:
//
//   - any YAML parse failure aborts the whole load with a path-tagged
//     error (the operator needs to know which file is broken).
//   - duplicate Profile.ID or Launch.ID is an error (silent collision
//     would mask a misconfigured catalog).
//   - a missing root → empty catalog (NOT an error).
func LoadCatalog(root string) (*Catalog, error) {
	if root == "" {
		return &Catalog{
			Profiles: map[string]Profile{},
			Launches: map[string]Launch{},
		}, nil
	}
	// Normalize to an absolute, cleaned path so the catalog Root is
	// stable regardless of the caller's cwd — see PR #169 round 1.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("bootprofile: resolve catalog root %s: %w", root, err)
	}
	root = filepath.Clean(absRoot)
	cat := &Catalog{
		Root:     root,
		Profiles: map[string]Profile{},
		Launches: map[string]Launch{},
	}
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, nil
		}
		return nil, fmt.Errorf("bootprofile: stat catalog root %s: %w", root, err)
	}

	profDir := filepath.Join(root, profilesSubdir)
	if err := loadDir(profDir, func(path string) error {
		p, err := LoadProfile(path)
		if err != nil {
			return err
		}
		if _, dup := cat.Profiles[p.ID]; dup {
			return fmt.Errorf("bootprofile: duplicate profile id %q (second occurrence: %s)", p.ID, path)
		}
		cat.Profiles[p.ID] = p
		return nil
	}); err != nil {
		return nil, err
	}

	launchDir := filepath.Join(root, launchesSubdir)
	if err := loadDir(launchDir, func(path string) error {
		l, err := LoadLaunch(path)
		if err != nil {
			return err
		}
		if _, dup := cat.Launches[l.ID]; dup {
			return fmt.Errorf("bootprofile: duplicate launch id %q (second occurrence: %s)", l.ID, path)
		}
		cat.Launches[l.ID] = l
		return nil
	}); err != nil {
		return nil, err
	}

	return cat, nil
}

// loadDir iterates *.yaml files in dir, invoking fn for each. A missing
// directory is silently skipped — the subdirectories are optional.
func loadDir(dir string, fn func(path string) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("bootprofile: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		if err := fn(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// LoadProfile reads and parses a single boot profile YAML, validating
// required fields. The error message always includes the file path so
// the operator can locate the failure source without grepping logs.
func LoadProfile(path string) (Profile, error) {
	b, err := readFile(path)
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := yaml.Unmarshal(b, &p); err != nil {
		return Profile{}, fmt.Errorf("bootprofile: parse profile %s: %w", path, err)
	}
	if err := validateProfile(p, path); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// LoadLaunch reads and parses a single launch profile YAML, validating
// required fields.
func LoadLaunch(path string) (Launch, error) {
	b, err := readFile(path)
	if err != nil {
		return Launch{}, err
	}
	var l Launch
	if err := yaml.Unmarshal(b, &l); err != nil {
		return Launch{}, fmt.Errorf("bootprofile: parse launch %s: %w", path, err)
	}
	if err := validateLaunch(l, path); err != nil {
		return Launch{}, err
	}
	return l, nil
}

// validateProfile enforces the minimum required fields. ID is required
// because the catalog keys by it; Identity.LineageAlias is required
// because §1 of the canonical boot prompt has no fallback. Slot bodies
// are validated lazily during Compile — slot-by-slot errors are easier
// to act on than a single aggregated failure at load time.
func validateProfile(p Profile, path string) error {
	if p.ID == "" {
		return fmt.Errorf("bootprofile: profile %s: missing required field 'id'", path)
	}
	if p.Identity.LineageAlias == "" {
		return fmt.Errorf("bootprofile: profile %s: missing required field 'identity.lineage_alias'", path)
	}
	return nil
}

// validateLaunch enforces the minimum required fields. Provider is
// required because every launch must route to a CLI adapter — the
// dropdown / runtime hookup tickets (0047 / 0048) both consume it.
func validateLaunch(l Launch, path string) error {
	if l.ID == "" {
		return fmt.Errorf("bootprofile: launch %s: missing required field 'id'", path)
	}
	if l.Provider == "" {
		return fmt.Errorf("bootprofile: launch %s: missing required field 'provider'", path)
	}
	return nil
}

// readFile wraps os.ReadFile with a bootprofile-tagged error so the
// caller can distinguish "file IO failed" from "YAML parse failed"
// without inspecting wrapped error types.
func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // catalog-sourced path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("bootprofile: file not found: %s: %w", path, err)
		}
		return nil, fmt.Errorf("bootprofile: read %s: %w", path, err)
	}
	return b, nil
}
