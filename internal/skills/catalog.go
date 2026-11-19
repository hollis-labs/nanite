package skills

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/hollis-labs/libs/agent/agentdir"
)

type Entry struct {
	Skill
	Dir string
}

type Catalog map[string]Entry

func NewCatalog(authored map[string]agentdir.SkillRef, host []Skill) Catalog {
	out := make(Catalog, len(authored)+len(host))
	for _, skill := range host {
		if _, exists := out[skill.Name]; !exists {
			out[skill.Name] = Entry{Skill: skill}
		}
	}
	for _, ref := range authored {
		out[ref.Name] = Entry{Skill: Skill{Name: ref.Name, Description: ref.Description, Body: ref.Body}, Dir: ref.Dir}
	}
	return out
}

func (c Catalog) Listing() string {
	var lines []string
	for _, name := range slices.Sorted(maps.Keys(c)) {
		skill := c[name]
		lines = append(lines, "- "+oneLine(skill.Name)+": "+oneLine(skill.Description))
	}
	return strings.Join(lines, "\n")
}

func (c Catalog) Read(name string) (string, error) {
	skill, ok := c[name]
	if !ok {
		return "", fmt.Errorf("skill %q is unavailable", name)
	}
	if skill.Dir == "" {
		return skill.Body, nil
	}
	return fmt.Sprintf("Skill directory: %s\n\n%s", skill.Dir, skill.Body), nil
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }
