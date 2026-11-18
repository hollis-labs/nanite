package definition

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hollis-labs/libs/agent/agentdir"
)

type Context struct{ Name, Text string }

type Definition struct {
	Snapshot agentdir.Snapshot
	Runtime  agentdir.Runtime
	Skills   map[string]agentdir.SkillRef
	Context  []Context
	Rules    []byte
}

func Load(dir, globalSkillsDir string) (Definition, error) {
	snap, err := agentdir.Load(dir)
	if err != nil {
		return Definition{}, err
	}
	out := Definition{Snapshot: snap, Runtime: snap.Profile.Runtime, Skills: make(map[string]agentdir.SkillRef)}
	if out.Runtime.Provider == "" {
		out.Runtime.Provider = "anthropic"
	}
	if out.Runtime.Model == "" {
		out.Runtime.Model = os.Getenv("NANITE_DEFAULT_MODEL")
		if out.Runtime.Model == "" {
			out.Runtime.Model = "claude-sonnet-5-5"
		}
	}
	if out.Runtime.MaxSteps == 0 {
		out.Runtime.MaxSteps = 50
	}
	if out.Runtime.ToolConcurrency == 0 {
		out.Runtime.ToolConcurrency = 4
	}
	if globalSkillsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Definition{}, err
		}
		globalSkillsDir = filepath.Join(home, ".nanite", "skills")
	}
	if globalSkillsDir != "-" {
		global, err := agentdir.ListSkills(globalSkillsDir)
		if err != nil {
			return Definition{}, fmt.Errorf("load global skills: %w", err)
		}
		for _, ref := range global {
			out.Skills[ref.Name] = ref
		}
	}
	for _, ref := range snap.Skills {
		out.Skills[ref.Name] = ref
	}
	for _, file := range snap.Context {
		text, err := readAuthored(file.Path)
		if err != nil {
			return Definition{}, err
		}
		out.Context = append(out.Context, Context{Name: file.Name, Text: string(text)})
	}
	if snap.PermissionsFile != "" {
		out.Rules, err = readAuthored(snap.PermissionsFile)
		if err != nil {
			return Definition{}, err
		}
	}
	return out, nil
}

func readAuthored(path string) ([]byte, error) {
	const limit = 1 << 20
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file at most 1 MiB", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s changed while opening", path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("%s exceeds 1 MiB", path)
	}
	return raw, nil
}
