package registryagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/nanite/agent/authored"
	"github.com/hollis-labs/nanite/internal/agentlock"
	"github.com/hollis-labs/nanite/toolregistry"
)

// Options controls unmanaged-folder replacement and package transport. Force
// never bypasses staleness checks or runtime locks. Existing state is preserved.
type Options struct {
	Force bool
	Tools *toolregistry.Client
}

// Manifest records the exact input revisions and payload digests.
type Manifest struct {
	Version        int         `json:"version"`
	Agent          Reference   `json:"agent"`
	Sources        []Source    `json:"sources"`
	Tools          []Reference `json:"tools,omitempty"`
	Roles          []string    `json:"roles,omitempty"`
	References     []Reference `json:"references,omitempty"`
	MaterializedAt time.Time   `json:"materialized_at"`
}

// Result reports staleness and the retained prior directory after replacement.
type Result struct {
	Manifest Manifest
	Changed  bool
	Backup   string
}

const manifestPath = ".nanite/materialized.json"

var fileName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Materialize stages and validates one agent folder before publishing it. It
// refuses symlink destinations and unowned folders unless Force is explicit.
// Replacing a folder uses an atomic directory exchange where supported.
func Materialize(ctx context.Context, resolver Resolver, agentRef Reference, dir string, options ...Options) (Result, error) {
	var result Result
	if resolver == nil || agentRef.Kind != "agent" {
		return result, fmt.Errorf("registryagent: agent resolver and reference required")
	}
	if len(options) > 1 {
		return result, fmt.Errorf("registryagent: at most one Options value")
	}
	var opts Options
	if len(options) == 1 {
		opts = options[0]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return result, err
	}
	parent, base := filepath.Dir(abs), filepath.Base(abs)
	if parent == abs {
		return result, fmt.Errorf("registryagent: refusing filesystem root")
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	guard, err := lock(ctx, root, ".nanite-materialize-"+base+".lock")
	if err != nil {
		return result, err
	}
	defer func() { _ = guard.Close() }()
	exists := false
	managed := false
	var before os.FileInfo
	var previous Manifest
	if before, err = root.Lstat(base); err == nil {
		exists = true
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("registryagent: destination must be a real directory")
		}
		raw, e := readBounded(root, filepath.Join(base, manifestPath))
		managed = e == nil && len(raw) <= maxRecordBytes && json.Unmarshal(raw, &previous) == nil && previous.Version == 1 && sameRef(previous.Agent, agentRef)
		if !managed && !opts.Force {
			return result, fmt.Errorf("registryagent: destination was not materialized for this agent; use force explicitly")
		}

	} else if !os.IsNotExist(err) {
		return result, err
	}
	stagePrefix := ".nanite-stage-" + base + "-"
	if err := cleanStages(root, stagePrefix); err != nil {
		return result, err
	}
	stage := stagePrefix + rand.Text()
	if err := root.Mkdir(stage, 0700); err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = root.RemoveAll(stage)
		}
	}()
	staged, err := root.OpenRoot(stage)
	if err != nil {
		return result, err
	}
	defer func() { _ = staged.Close() }()
	if exists {
		pins, e := readBounded(root, filepath.Join(base, "tools.lock.json"))
		if e == nil {
			if len(pins) > maxRecordBytes {
				return result, fmt.Errorf("registryagent: tools lock too large")
			}
			if writeErr := staged.WriteFile("tools.lock.json", pins, 0600); writeErr != nil {
				return result, writeErr
			}
		} else if !os.IsNotExist(e) {
			return result, e
		}
	}
	manifest, err := build(ctx, resolver, agentRef, filepath.Join(parent, stage), staged, opts)
	if err != nil {
		return result, err
	}
	if managed && reflect.DeepEqual(previous.Sources, manifest.Sources) && reflect.DeepEqual(previous.Tools, manifest.Tools) {
		return Result{Manifest: previous}, nil
	}
	publication, err := agentlock.Publication(abs, false)
	if err != nil {
		return result, err
	}
	defer func() { _ = publication.Close() }()
	if exists {
		current, err := root.Lstat(base)
		if err != nil || !os.SameFile(before, current) {
			return result, fmt.Errorf("registryagent: destination changed during materialization")
		}
		stateLock, err := preserveState(ctx, root, base, staged)
		if err != nil {
			return result, err
		}
		defer func() { _ = stateLock.Close() }()
	}
	manifest.MaterializedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return result, err
	}
	if len(raw) > maxRecordBytes {
		return result, fmt.Errorf("registryagent: materialization manifest exceeds size limit")
	}
	if err := staged.MkdirAll(".nanite", 0700); err != nil {
		return result, err
	}
	if err := staged.WriteFile(manifestPath, raw, 0600); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Recheck destination identity after resolver calls and validation.
	after, e := root.Lstat(base)
	if exists {
		if e != nil || !os.SameFile(before, after) {
			return result, fmt.Errorf("registryagent: destination changed during materialization")
		}
	} else if !os.IsNotExist(e) {
		return result, fmt.Errorf("registryagent: destination appeared during materialization")
	}
	parentFile, err := root.Open(".")
	if err != nil {
		return result, err
	}
	defer func() { _ = parentFile.Close() }()
	if err := publish(parentFile, stage, base, exists); err != nil {
		return result, err
	}
	result = Result{Manifest: manifest, Changed: true}
	if exists {
		keep = true
		backup := ".nanite-backup-" + rand.Text()
		result.Backup = filepath.Join(parent, stage)
		if err := root.Rename(stage, backup); err != nil {
			return result, err
		}
		result.Backup = filepath.Join(parent, backup)
	}
	if err := parentFile.Sync(); err != nil {
		return result, err
	}
	return result, nil
}

func build(ctx context.Context, resolver Resolver, ref Reference, dir string, root *os.Root, opts Options) (Manifest, error) {
	m := Manifest{Version: 1, Agent: ref}
	record, _, src, err := fetch(ctx, resolver, ref, "")
	if err != nil {
		return m, err
	}
	m.Sources = append(m.Sources, src)
	var a struct {
		Name, Summary, Charter                       string
		Roles                                        []string
		Skills, Context, Variants, Tools, References []Reference
		LaunchProfile                                versioned `json:"launch_profile"`
	}
	if err := json.Unmarshal(record.Payload, &a); err != nil {
		return m, err
	}
	if !fileName.MatchString(a.Name) {
		return m, fmt.Errorf("registryagent: agent name must be a safe folder name")
	}
	purpose := a.Summary
	if purpose == "" {
		purpose, _, _ = strings.Cut(strings.TrimSpace(a.Charter), "\n")
	}
	front := map[string]any{"schema_version": "2", "definition_id": "def:" + ref.ID, "revision": record.Revision, "name": a.Name, "description": purpose, "behavior": map[string]any{"purpose": purpose}, "requirements": map[string]any{}, "harness_profile": map[string]any{"permissions": map[string]string{"profile": "default"}}, "continuity": map[string]string{"mode": "durable"}}
	if err := writeMarkdown(root, "agent.md", front, a.Charter); err != nil {
		return m, err
	}
	_, profile, profileSource, err := fetch(ctx, resolver, a.LaunchProfile.Reference, a.LaunchProfile.Revision)
	if err != nil {
		return m, fmt.Errorf("registryagent: launch profile: %w", err)
	}
	m.Sources = append(m.Sources, profileSource)
	host, _ := profile["host"].(string)
	config := map[string]any{"kind": "embedded", "lifetime": "application", "application_id": a.Name}
	switch {
	case strings.HasPrefix(host, "surface-module:"), host == "nanite-service":
	case host == "nanite-cli":
		config = map[string]any{"kind": "stable", "lifetime": "on-demand", "seat_id": a.Name}
	default:
		return m, fmt.Errorf("registryagent: supported launch profile host required")
	}
	runtime := map[string]any{}
	if r, ok := profile["runtime"].(map[string]any); ok {
		for k, v := range r {
			if k == "settings" {
				for key, value := range v.(map[string]any) {
					runtime[key] = value
				}
			} else {
				runtime[k] = v
			}
		}
	}
	config["runtime"] = runtime
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return m, err
	}
	if err := root.WriteFile("host.yaml", raw, 0600); err != nil {
		return m, err
	}
	written := map[string]bool{}
	total := len(record.Payload)
	for _, group := range []struct {
		refs []Reference
		kind string
	}{{a.Skills, "skills"}, {a.Context, "context"}, {a.Variants, "variants"}} {
		for _, child := range group.refs {
			_, data, s, err := fetch(ctx, resolver, child, "")
			if err != nil {
				return m, err
			}
			m.Sources = append(m.Sources, s)
			name, _ := data["name"].(string)
			if !fileName.MatchString(name) {
				return m, fmt.Errorf("registryagent: unsafe content name")
			}
			body, _ := data["body"].(string)
			path := group.kind + "/" + name + ".md"
			if group.kind == "skills" {
				if child.Kind != "skill" {
					return m, fmt.Errorf("registryagent: expected skill")
				}
				body, _ = data["instructions"].(string)
				path = "skills/" + name + "/SKILL.md"
			} else if child.Kind != "document" {
				return m, fmt.Errorf("registryagent: context and variant references must resolve to documents")
			}
			total += len(body)
			if total > 8<<20 {
				return m, fmt.Errorf("registryagent: authored content exceeds total size limit")
			}
			if written[path] {
				return m, fmt.Errorf("registryagent: duplicate output name")
			}
			written[path] = true
			if err := root.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return m, err
			}
			if group.kind == "skills" {
				summary, _ := data["summary"].(string)
				if summary == "" {
					summary = name
				}
				err = writeMarkdown(root, path, map[string]string{"name": name, "description": summary}, body)
			} else {
				err = root.WriteFile(path, []byte(body), 0600)
			}
			if err != nil {
				return m, err
			}
		}
	}
	m.Tools = a.Tools
	m.Roles = a.Roles
	m.References = a.References
	for _, tool := range a.Tools {
		if tool.Kind != "tool-package" {
			continue
		}
		if tool.Authority != ref.Authority {
			return m, fmt.Errorf("registryagent: tool package authority does not match configured registry")
		}
		if opts.Tools == nil {
			return m, fmt.Errorf("registryagent: tool package transport required")
		}
		installed, err := toolregistry.Install(ctx, *opts.Tools, dir, tool.ID)
		if err != nil {
			return m, err
		}
		source := Source{Reference: tool, Revision: installed.Version, Digest: installed.Digest}
		if reader, ok := opts.Tools.Records.(interface{ Publication() string }); ok {
			source.Publication = reader.Publication()
			if source.Publication != "draft" && source.Publication != "published" {
				return m, fmt.Errorf("registryagent: invalid tool publication state")
			}
		}
		m.Sources = append(m.Sources, source)
	}
	if _, err := agentdir.Load(dir); err != nil {
		return m, err
	}
	if _, err := authored.LoadVariants(dir); err != nil {
		return m, err
	}
	if _, err := authored.LoadTools(dir); err != nil {
		return m, err
	}
	return m, nil
}

func writeMarkdown(root *os.Root, path string, front any, body string) error {
	raw, err := json.MarshalIndent(front, "", "  ")
	if err != nil {
		return err
	}
	return root.WriteFile(path, []byte("---\n"+string(raw)+"\n---\n"+body+"\n"), 0600)
}

func lock(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("registryagent: lock must be a regular file")
	}
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) // #nosec G115 -- Unix file descriptor.
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
	_ = f.Close()
	return nil, err
}

func readBounded(root *os.Root, path string) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRecordBytes {
		return nil, fmt.Errorf("registryagent: metadata exceeds size limit")
	}
	return data, nil
}

// Only this destination's reserved staging directories are cleaned under its
// materializer lock. Backups use a different prefix and are never removed.
func cleanStages(root *os.Root, prefix string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && isStage(entry.Name(), prefix) {
			if err := root.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

// isStage matches exactly prefix + a rand.Text token (26 base32 characters),
// so a sibling destination whose name extends this one's is never matched.
func isStage(name, prefix string) bool {
	token, ok := strings.CutPrefix(name, prefix)
	if !ok || len(token) != 26 {
		return false
	}
	for _, c := range token {
		if (c < 'A' || c > 'Z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}
