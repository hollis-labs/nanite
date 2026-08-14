package agentworkflow

import "fmt"

// Levels partitions steps into topological execution levels: every step in
// level N depends only on steps in levels 0..N-1 (or has no dependencies, in
// which case it lands in level 0). Steps within a level share no dependency
// edges with each other, so a WorkflowEngine may run them concurrently.
// Returns an error if steps reference an unknown DependsOn id or if the
// dependency graph contains a cycle (design doc: "DAG only, no cycles").
//
// Kahn's algorithm — the same shape apps/hadron's internal/pipeline.TopoSort
// uses for its per-blueprint leveling, applied here at per-step granularity.
func Levels(steps []StepDefinition) ([][]StepDefinition, error) {
	byID := make(map[string]StepDefinition, len(steps))
	for _, s := range steps {
		if _, dup := byID[s.ID]; dup {
			return nil, fmt.Errorf("agentworkflow: duplicate step id %q", s.ID)
		}
		byID[s.ID] = s
	}
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("agentworkflow: step %q depends on unknown step %q", s.ID, dep)
			}
		}
	}

	inDegree := make(map[string]int, len(steps))
	dependents := make(map[string][]string, len(steps))
	for _, s := range steps {
		inDegree[s.ID] = len(s.DependsOn)
		for _, dep := range s.DependsOn {
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	remaining := len(steps)
	var levels [][]StepDefinition
	for remaining > 0 {
		var level []StepDefinition
		for _, s := range steps {
			if inDegree[s.ID] == 0 {
				level = append(level, s)
			}
		}
		if len(level) == 0 {
			// Every remaining step has an unsatisfied in-degree with no
			// ready candidates left — the only way that happens is a cycle.
			return nil, fmt.Errorf("agentworkflow: dependency cycle detected among remaining steps")
		}
		levels = append(levels, level)
		for _, s := range level {
			// -1 sentinel removes s from future in-degree scans without
			// mutating the steps slice.
			inDegree[s.ID] = -1
			remaining--
			for _, dep := range dependents[s.ID] {
				if inDegree[dep] > 0 {
					inDegree[dep]--
				}
			}
		}
	}
	return levels, nil
}
