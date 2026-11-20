package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/agent/steer"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/subagent"
)

type profileKey struct{}
type turnProfile struct {
	owner       *Engine
	dir, digest string
	definition  definition.Definition
	variants    map[string]subagent.Variant
	packages    map[string]authored.ToolPackage
	steering    *Steering
}

func profileFrom(ctx context.Context) *turnProfile {
	p, _ := ctx.Value(profileKey{}).(*turnProfile)
	return p
}
func (e *Engine) profileDir(ctx context.Context) string {
	if p := profileFrom(ctx); p != nil {
		return p.dir
	}
	return e.cfg.Dir
}
func (e *Engine) steeringConfig(ctx context.Context) *Steering {
	if p := profileFrom(ctx); p != nil {
		return p.steering
	}
	return e.cfg.Steering
}

// prepareProfile completes all fallible profile validation before a run is
// registered. Descendants keep the parent's snapshot, even after a switch.
func (e *Engine) prepareProfile(ctx context.Context, in *Input) (context.Context, error) {
	headers, err := station.CopyHeaders(in.Headers)
	if err != nil {
		return ctx, err
	}
	if in.Headers == nil {
		headers = maps.Clone(station.Headers(ctx))
	}
	in.Headers = headers
	ctx = station.WithHeaders(ctx, headers)
	if p := profileFrom(ctx); p != nil && (in.AgentDir == "" || in.AgentDir == p.dir) {
		if p.owner != e {
			return ctx, errors.New("turn context belongs to another agent")
		}
		if in.Steer != nil && (in.Steer.profileDir != p.dir || in.Steer.profileDigest != p.digest) {
			return ctx, errors.New("nanite: Input.Steer was prepared for another profile")
		}
		in.AgentDir = p.dir
		return ctx, nil
	}
	if in.AgentDir == "" {
		if in.Steer != nil && in.Steer.profileDir != "" {
			return ctx, errors.New("nanite: Input.Steer was prepared for another profile")
		}
		return ctx, nil
	}
	dir, err := filepath.Abs(in.AgentDir)
	if err != nil {
		return ctx, err
	}
	p, err := e.loadProfile(dir)
	if err != nil {
		return ctx, fmt.Errorf("turn profile %q: %w", dir, err)
	}
	in.AgentDir = dir
	if in.Steer != nil && (in.Steer.profileDir != dir || in.Steer.profileDigest != p.digest) {
		return ctx, errors.New("nanite: Input.Steer was prepared for another profile")
	}
	return context.WithValue(ctx, profileKey{}, p), nil
}

type profileFile struct {
	data []byte
	mode fs.FileMode
}

func readProfile(dir string) (map[string]profileFile, string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = root.Close() }()
	files := map[string]profileFile{}
	hash := sha256.New()
	total := 0
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: profile links are not supported", name)
		}
		if name == "." {
			return nil
		}
		top, _, _ := strings.Cut(name, "/")
		switch top {
		case "host.yaml", "providers.yaml", "identity.yaml", "permissions.yaml", ".mcp.json":
			return fmt.Errorf("%s is host-owned and cannot occur in a turn profile", top)
		}
		if top == "evals" || name == "README.md" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch top {
		case "agent.md", "context", "skills", "variants", "tools", "steering.json":
		default:
			return fmt.Errorf("%s is not part of a turn profile", name)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return fmt.Errorf("%s must be a regular file of at most 1 MiB", name)
		}
		data, err := root.ReadFile(name)
		if err != nil {
			return err
		}
		total += len(data)
		if total > 4<<20 {
			return errors.New("profile exceeds 4 MiB")
		}
		files[name] = profileFile{data, info.Mode().Perm()}
		fmt.Fprintf(hash, "%d:%s:%d:%d:", len(name), name, info.Mode().Perm(), len(data))
		_, _ = hash.Write(data)
		return nil
	})
	return files, hex.EncodeToString(hash.Sum(nil)), err
}

func (e *Engine) loadProfile(dir string) (*turnProfile, error) {
	files, digest, err := readProfile(dir)
	if err != nil {
		return nil, err
	}
	e.profileMu.Lock()
	defer e.profileMu.Unlock()
	if p := e.profiles[dir]; p != nil && p.digest == digest {
		return p, nil
	}
	// agentdir.Load requires host.yaml. Validate the captured files against a
	// stand-in host, then retain only the authored fields allowed in a profile.
	tmp, err := os.MkdirTemp("", "nanite-profile-check-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for name, file := range files {
		path := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, file.data, file.mode); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "host.yaml"), []byte("kind: embedded\nlifetime: application\napplication_id: profile-check\n"), 0600); err != nil {
		return nil, err
	}
	snap, err := agentdir.Load(tmp)
	if err != nil {
		return nil, err
	}
	variants, err := subagent.Load(tmp)
	if err != nil {
		return nil, err
	}
	packages, err := authored.LoadTools(tmp, e.cfg.ToolPackageEnv...)
	if err != nil {
		return nil, err
	}
	cfg, err := e.profileSteering(files["steering.json"].data)
	if err != nil {
		return nil, fmt.Errorf("steering.json: %w", err)
	}
	def := e.profileBase
	def.Snapshot.Definition = snap.Definition
	def.Snapshot.Directory = dir
	def.Snapshot.Digest = digest
	// A composed profile owns authored skills; global skills still apply.
	def.Skills = maps.Clone(e.profileBase.GlobalSkills)
	def.Snapshot.Skills = snap.Skills
	for i := range def.Snapshot.Skills {
		ref := &def.Snapshot.Skills[i]
		rel, err := filepath.Rel(tmp, ref.Dir)
		if err != nil {
			return nil, err
		}
		ref.Dir = filepath.Join(dir, rel)
		def.Skills[ref.Name] = *ref
	}
	def.Context = nil
	def.Snapshot.Context = snap.Context
	for i := range def.Snapshot.Context {
		c := &def.Snapshot.Context[i]
		c.Path = filepath.Join(dir, "context", c.Name)
		def.Context = append(def.Context, definition.Context{Name: c.Name, Text: c.Body})
	}
	p := &turnProfile{owner: e, dir: dir, digest: digest, definition: def, variants: variants, packages: packages, steering: cfg}
	if len(e.profiles) >= 32 {
		clear(e.profiles)
	}
	e.profiles[dir] = p
	return p, nil
}

func (e *Engine) profileSteering(raw []byte) (*Steering, error) {
	if len(raw) == 0 {
		return e.cfg.Steering, nil
	}
	var data struct {
		Format   int            `json:"workbench_format,omitempty"`
		Presets  []steer.Preset `json:"presets,omitempty"`
		Reflexes []steer.Reflex `json:"reflexes,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&data); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("expected one JSON object")
	}
	cfg := Steering{}
	if e.cfg.Steering != nil {
		cfg = *e.cfg.Steering
	}
	reg, err := steer.NewRegistry(data.Presets...)
	if err != nil {
		return nil, err
	}
	if cfg.Registry != nil {
		for _, name := range cfg.Registry.Names() {
			p, origin, _ := cfg.Registry.Get(name)
			if err := reg.Add(origin.Layer, origin.Source, p); err != nil {
				return nil, err
			}
		}
		if name := cfg.Registry.Default(); name != "" {
			if err := reg.SetDefault(name); err != nil {
				return nil, err
			}
		}
	}
	for _, preset := range data.Presets {
		if err := reg.Add(steer.LayerModule, "turn-profile", preset); err != nil {
			return nil, err
		}
	}
	cfg.Registry = reg
	if cfg.Vocabulary == nil {
		cfg.Vocabulary = steer.NewVocabulary()
	}
	seen := map[string]bool{}
	for _, rx := range data.Reflexes {
		if err := rx.Validate(cfg.Vocabulary); err != nil {
			return nil, err
		}
		if seen[rx.Name] {
			return nil, fmt.Errorf("duplicate reflex %q", rx.Name)
		}
		seen[rx.Name] = true
	}
	for _, preset := range data.Presets {
		for _, name := range preset.Reflexes {
			if !seen[name] {
				return nil, fmt.Errorf("preset %q names undefined reflex %q", preset.Name, name)
			}
		}
	}
	host := cfg.Reflexes
	cfg.Reflexes = func() []steer.Reflex {
		out := append([]steer.Reflex(nil), data.Reflexes...)
		if host != nil {
			out = append(out, host()...)
		}
		return out
	}
	return &cfg, cfg.validate()
}

// TurnContext validates a host's scope before planning or dispatching work.
func (e *Engine) TurnContext(ctx context.Context, in Input) (context.Context, error) {
	return e.prepareProfile(ctx, &in)
}
