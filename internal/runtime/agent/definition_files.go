package agent

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
)

// definitionFiles returns the pinned reference files of the definition behind
// params.AgentProfile (requirements.resources and harness_profile.context
// sources), keyed by boot-dir-relative path. Every key sits under
// agentpolicy.FilesRoot; anything else is dropped rather than planted, so a
// definition can never reach a provider file such as CLAUDE.md or .mcp.json.
func definitionFiles(params SetupParams) map[string][]byte {
	if params.AgentProfile == nil || len(params.AgentProfile.DefinitionFiles) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(params.AgentProfile.DefinitionFiles))
	for rel, content := range params.AgentProfile.DefinitionFiles {
		if path.Clean(rel) != rel || !strings.HasPrefix(rel, agentpolicy.FilesRoot+"/") {
			continue
		}
		out[rel] = content
	}
	return out
}

// definitionFilesFor is definitionFiles plus, for a runtime whose shared layout
// row places skills (sharedSkillRoot), a copy of every granted skill the
// definition delivers under agent/skills/<name>/ at that runtime's own skill
// location, <root>/<name>/..., so the CLI finds it where it looks for skills.
// A path already planted by the profile's own skills is not replaced.
func definitionFilesFor(providerName string, params SetupParams) map[string][]byte {
	out := definitionFiles(params)
	dest := skillDestPrefixesFor(providerName)
	if dest == nil {
		return out
	}
	const prefix = agentpolicy.FilesRoot + "/" + agentpolicy.SkillsDir + "/"
	for rel, content := range definitionFiles(params) {
		rest, ok := strings.CutPrefix(rel, prefix)
		if !ok {
			continue
		}
		name, file, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		prefixes, ok := dest(name)
		if !ok {
			continue
		}
		for _, p := range prefixes {
			if target := path.Join(p, file); path.Clean(target) == target {
				if _, taken := out[target]; !taken {
					out[target] = content
				}
			}
		}
	}
	return out
}

// PlantDefinitionFiles writes a definition's pinned reference files under
// <ws.Root>/agent/ so a native-loop session finds them where a CLI session finds
// them in its boot root. Keys must be clean, relative and under the files root;
// anything else is refused. Existing files are rewritten (the bytes are pinned),
// so it is idempotent across turns.
func PlantDefinitionFiles(ws *Workspace, files map[string][]byte) error {
	if ws == nil || len(files) == 0 {
		return nil
	}
	for rel, content := range files {
		if path.Clean(rel) != rel || !strings.HasPrefix(rel, agentpolicy.FilesRoot+"/") {
			return fmt.Errorf("agent.PlantDefinitionFiles: %q is not a path under %s/", rel, agentpolicy.FilesRoot)
		}
		dest := filepath.Join(ws.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dest, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}
