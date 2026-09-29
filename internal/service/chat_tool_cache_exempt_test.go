package service

import "testing"

func TestIsCacheExemptTool(t *testing.T) {
	cases := map[string]bool{
		"tool_describe":      true,
		"tool_validate":      true,
		"fetch_tool_result":  true,
		"search_tool_result": true,
		"card_show":          false,
		"lesson_capture":     false,
		"dev_read":           false,
		"":                   false,
	}
	for name, want := range cases {
		if got := isCacheExemptTool(name); got != want {
			t.Errorf("isCacheExemptTool(%q) = %v, want %v", name, got, want)
		}
	}
}

// D-37 (CW-20260919-0012): the skill listing carries name + description
// only, so a loaded skill body must go through the result cache — preview
// plus a tool_result:// pointer when large. Exempting skill_get would put
// whole bodies in context.
func TestSkillGetResultsAreCached(t *testing.T) {
	for _, name := range []string{"skill_get", "skill_list"} {
		if isCacheExemptTool(name) {
			t.Errorf("%s must not be cache-exempt", name)
		}
	}
}
