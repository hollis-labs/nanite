package service

import "testing"

func TestIsCacheExemptTool(t *testing.T) {
	cases := map[string]bool{
		"tool_describe": true,
		"tool_validate":      true,
		"fetch_tool_result":    true,
		"search_tool_result":   true,
		"card_show":     false,
		"lesson_capture":      false,
		"dev_read":             false,
		"":                     false,
	}
	for name, want := range cases {
		if got := isCacheExemptTool(name); got != want {
			t.Errorf("isCacheExemptTool(%q) = %v, want %v", name, got, want)
		}
	}
}
