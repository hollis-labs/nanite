package harnessprofile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultProfileName is the profile used when a run selects none and the app
// configures none.
const DefaultProfileName = "default"

// Registry finds profiles: the embedded built-ins plus user files in a
// directory. A user file is re-read when it changes, so editing a profile takes
// effect on the next run without a restart. A user file may not reuse a
// built-in's name.
type Registry struct {
	dir         string
	defaultName string
	builtin     map[string]*Profile

	mu    sync.Mutex
	files map[string]fileEntry
}

type fileEntry struct {
	modTime time.Time
	size    int64
	profile *Profile
}

// NewRegistry builds a registry over dir (which may be empty or absent) with
// defaultName as the profile used when none is selected ("" means "default").
// The default name is checked now so a bad configuration fails at startup.
func NewRegistry(dir, defaultName string) (*Registry, error) {
	r := &Registry{dir: dir, defaultName: defaultName, builtin: map[string]*Profile{}, files: map[string]fileEntry{}}
	if r.defaultName == "" {
		r.defaultName = DefaultProfileName
	}
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		data, err := builtinFS.ReadFile("builtin/" + e.Name())
		if err != nil {
			return nil, err
		}
		p, err := ParseProfile(data)
		if err != nil {
			return nil, fmt.Errorf("built-in %s: %w", e.Name(), err)
		}
		if p.Name+".yaml" != e.Name() {
			return nil, fmt.Errorf("built-in %s: name %q does not match the file", e.Name(), p.Name)
		}
		r.builtin[p.Name] = p
	}
	if r.dir != "" {
		if entries, err := os.ReadDir(r.dir); err == nil {
			for _, e := range entries {
				if n, ok := profileFileName(e.Name()); ok && !e.IsDir() {
					if _, clash := r.builtin[n]; clash {
						return nil, fmt.Errorf("profile file %s: %q is a built-in profile and cannot be redefined", filepath.Join(r.dir, e.Name()), n)
					}
				}
			}
		}
	}
	if _, err := r.chain(r.defaultName); err != nil {
		return nil, fmt.Errorf("default profile: %w", err)
	}
	return r, nil
}

// DefaultName is the profile a run without a selection uses.
func (r *Registry) DefaultName() string { return r.defaultName }

// Names lists every profile that can currently be selected, built-ins and
// valid user files, sorted. Unreadable user files are omitted; Get reports why.
func (r *Registry) Names() []string {
	seen := map[string]bool{}
	for n := range r.builtin {
		seen[n] = true
	}
	if r.dir != "" {
		if entries, err := os.ReadDir(r.dir); err == nil {
			for _, e := range entries {
				if n, ok := profileFileName(e.Name()); ok && !e.IsDir() {
					seen[n] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func profileFileName(file string) (string, bool) {
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(file, ext) {
			return strings.TrimSuffix(file, ext), true
		}
	}
	return "", false
}

// chain returns name's extends chain, base first, or an error naming what is
// wrong: an unknown profile, an unreadable file, or a cycle.
func (r *Registry) chain(name string) ([]*Profile, error) {
	var rev []*Profile
	seen := map[string]bool{}
	for cur := name; cur != ""; {
		if seen[cur] {
			return nil, fmt.Errorf("profile %q: extends cycle through %q", name, cur)
		}
		seen[cur] = true
		p, err := r.load(cur)
		if err != nil {
			return nil, err
		}
		rev = append(rev, p)
		cur = p.Extends
	}
	out := make([]*Profile, len(rev))
	for i, p := range rev {
		out[len(rev)-1-i] = p
	}
	return out, nil
}

func (r *Registry) load(name string) (*Profile, error) {
	if p, ok := r.builtin[name]; ok {
		return p, nil
	}
	if r.dir == "" || !nameRE.MatchString(name) {
		return nil, r.unknown(name)
	}
	for _, ext := range []string{".yaml", ".yml"} {
		file := filepath.Join(r.dir, name+ext)
		info, err := os.Stat(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", name, err)
		}
		return r.loadFile(name, file, info)
	}
	return nil, r.unknown(name)
}

func (r *Registry) loadFile(name, file string, info fs.FileInfo) (*Profile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.files[file]; ok && e.modTime.Equal(info.ModTime()) && e.size == info.Size() {
		return e.profile, nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // file is the profiles directory joined with a name validated by nameRE
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}
	p, err := ParseProfile(data)
	if err != nil {
		return nil, fmt.Errorf("profile file %s: %w", file, err)
	}
	if p.Name != name {
		return nil, fmt.Errorf("profile file %s: name %q does not match the file name", file, p.Name)
	}
	r.files[file] = fileEntry{modTime: info.ModTime(), size: info.Size(), profile: p}
	return p, nil
}

func (r *Registry) unknown(name string) error {
	return fmt.Errorf("unknown harness profile %q (available: %s)", name, strings.Join(r.Names(), ", "))
}

// Exists reports whether name resolves to a valid profile chain.
func (r *Registry) Exists(name string) error {
	_, err := r.chain(name)
	return err
}
