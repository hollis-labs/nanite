package agentpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// SkillTreeFormat names the canonical encoding of a packaged skill. A
// definition pins a skill by the digest of this encoding, so the pin covers the
// whole folder (SKILL.md and every script or reference beside it), not SKILL.md
// alone.
const SkillTreeFormat = "nanite-skill-tree/v1"

// MaxSkillTreeBytes bounds an encoded tree; a definition resource may not
// exceed 1 MiB.
const MaxSkillTreeBytes = 1 << 20

type skillTree struct {
	Format string          `json:"format"`
	Files  []skillTreeFile `json:"files"`
}

type skillTreeFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// EncodeSkillTree is the canonical, deterministic encoding of files (paths
// relative to the skill folder, slash separated). The same files always give the
// same bytes, whatever order they were collected in.
func EncodeSkillTree(files map[string][]byte) ([]byte, error) {
	t := skillTree{Format: SkillTreeFormat, Files: make([]skillTreeFile, 0, len(files))}
	for p, c := range files {
		if err := checkSkillPath(p); err != nil {
			return nil, err
		}
		t.Files = append(t.Files, skillTreeFile{Path: p, Content: c})
	}
	if _, ok := files["SKILL.md"]; !ok {
		return nil, errors.New("a skill folder needs a SKILL.md")
	}
	sort.Slice(t.Files, func(i, j int) bool { return t.Files[i].Path < t.Files[j].Path })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(t); err != nil {
		return nil, err
	}
	if buf.Len() > MaxSkillTreeBytes {
		return nil, fmt.Errorf("skill folder encodes to %d bytes, over the %d byte limit", buf.Len(), MaxSkillTreeBytes)
	}
	return buf.Bytes(), nil
}

// DecodeSkillTree reads an encoded tree back into relative path -> bytes,
// refusing any path that is not clean, relative and inside the folder.
func DecodeSkillTree(data []byte) (map[string][]byte, error) {
	var t skillTree
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("skill tree: %w", err)
	}
	if t.Format != SkillTreeFormat {
		return nil, fmt.Errorf("skill tree: unsupported format %q", t.Format)
	}
	out := make(map[string][]byte, len(t.Files))
	for _, f := range t.Files {
		if err := checkSkillPath(f.Path); err != nil {
			return nil, err
		}
		if _, dup := out[f.Path]; dup {
			return nil, fmt.Errorf("skill tree: duplicate path %q", f.Path)
		}
		out[f.Path] = f.Content
	}
	if _, ok := out["SKILL.md"]; !ok {
		return nil, errors.New("skill tree: no SKILL.md")
	}
	return out, nil
}

func checkSkillPath(p string) error {
	if p == "" || path.Clean(p) != p || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "\\") {
		return fmt.Errorf("skill tree: %q is not a clean relative path", p)
	}
	return nil
}
