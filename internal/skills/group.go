package skills

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

type Skill struct{ Name, Description, Body string }

type Groups struct {
	mu     sync.RWMutex
	groups map[string][]Skill
}

func (g *Groups) SetGroup(group string, skills ...Skill) error {
	if strings.TrimSpace(group) == "" {
		return fmt.Errorf("skill group name required")
	}
	seen := make(map[string]bool, len(skills))
	for _, skill := range skills {
		if strings.TrimSpace(skill.Name) == "" || seen[skill.Name] {
			return fmt.Errorf("invalid or duplicate skill name %q", skill.Name)
		}
		seen[skill.Name] = true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.groups == nil {
		g.groups = make(map[string][]Skill)
	}
	g.groups[group] = slices.Clone(skills)
	return nil
}

func (g *Groups) RemoveGroup(group string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.groups, group)
}

// Listing returns names and descriptions for the skills slot.
func (g *Groups) Listing() string {
	var lines []string
	for _, skill := range g.Snapshot() {
		lines = append(lines, "- "+skill.Name+": "+skill.Description)
	}
	return strings.Join(lines, "\n")
}

// Snapshot returns host skills in stable group order.
func (g *Groups) Snapshot() []Skill {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Skill
	for _, group := range slices.Sorted(maps.Keys(g.groups)) {
		out = append(out, g.groups[group]...)
	}
	return out
}
