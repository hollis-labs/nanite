package builtin

import (
	"embed"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/skill"
)

//go:embed *.md
var builtinSkillsFS embed.FS

// BuiltinSkills returns all parsed built-in skill definitions.
func BuiltinSkills() ([]*skill.Definition, error) {
	entries, err := builtinSkillsFS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("builtin skills: read embedded dir: %w", err)
	}

	var defs []*skill.Definition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := builtinSkillsFS.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("builtin skills: read %s: %w", e.Name(), err)
		}
		def, err := skill.ParseMD(data)
		if err != nil {
			return nil, fmt.Errorf("builtin skills: parse %s: %w", e.Name(), err)
		}
		def.Source = "builtin"
		def.SourceRef = "embedded:" + e.Name()
		defs = append(defs, def)
	}
	return defs, nil
}
