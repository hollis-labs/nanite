package service

import "testing"

func TestIsCacheExemptTool(t *testing.T) {
	cases := map[string]bool{
		"nanite_tool_describe": true,
		"nanite_validate":      true,
		"fetch_tool_result":    true,
		"search_tool_result":   true,
		"nanite_show_card":     false,
		"nanite_remember":      false,
		"dev_read":             false,
		"":                     false,
	}
	for name, want := range cases {
		if got := isCacheExemptTool(name); got != want {
			t.Errorf("isCacheExemptTool(%q) = %v, want %v", name, got, want)
		}
	}
}
