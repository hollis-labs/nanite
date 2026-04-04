package plugin

import "testing"

func TestLoadType_Effective(t *testing.T) {
	tests := []struct {
		input LoadType
		want  LoadType
	}{
		{"", LoadTypeAuto},
		{LoadTypeAuto, LoadTypeAuto},
		{LoadTypeOptIn, LoadTypeOptIn},
	}
	for _, tt := range tests {
		if got := tt.input.Effective(); got != tt.want {
			t.Errorf("LoadType(%q).Effective() = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoadType_IsOptIn(t *testing.T) {
	if LoadTypeAuto.IsOptIn() {
		t.Error("LoadTypeAuto should not be opt-in")
	}
	if !LoadTypeOptIn.IsOptIn() {
		t.Error("LoadTypeOptIn should be opt-in")
	}
	if LoadType("").IsOptIn() {
		t.Error("zero value should not be opt-in")
	}
}

func TestPluginManifest_EffectiveLoadType(t *testing.T) {
	m := &PluginManifest{
		LoadType: LoadTypeOptIn,
		ToolOverrides: map[string]ToolLoadOverride{
			"special_tool": {LoadType: LoadTypeAuto},
		},
	}

	// Tool with override → uses override.
	if got := m.EffectiveLoadType("special_tool"); got != LoadTypeAuto {
		t.Errorf("special_tool: got %q, want %q", got, LoadTypeAuto)
	}

	// Tool without override → falls back to plugin default.
	if got := m.EffectiveLoadType("other_tool"); got != LoadTypeOptIn {
		t.Errorf("other_tool: got %q, want %q", got, LoadTypeOptIn)
	}

	// Nil overrides → plugin default.
	m2 := &PluginManifest{LoadType: LoadTypeAuto}
	if got := m2.EffectiveLoadType("any"); got != LoadTypeAuto {
		t.Errorf("nil overrides: got %q, want %q", got, LoadTypeAuto)
	}
}

func TestLoadTypeResolver_Resolve(t *testing.T) {
	resolver := NewLoadTypeResolver(
		LoadTypeLayer{Name: "session", Overrides: map[string]LoadType{
			"grep": LoadTypeAuto,
		}},
		LoadTypeLayer{Name: "project", Overrides: map[string]LoadType{
			"grep":  LoadTypeOptIn, // should be shadowed by session
			"write": LoadTypeOptIn,
		}},
		LoadTypeLayer{Name: "agent", Overrides: map[string]LoadType{
			"write": LoadTypeAuto, // should be shadowed by project
			"read":  LoadTypeOptIn,
		}},
		LoadTypeLayer{Name: "manifest", Overrides: map[string]LoadType{
			"read":   LoadTypeAuto, // should be shadowed by agent
			"search": LoadTypeOptIn,
		}},
	)

	tests := []struct {
		tool       string
		wantLT     LoadType
		wantSource string
	}{
		{"grep", LoadTypeAuto, "session"},     // session wins
		{"write", LoadTypeOptIn, "project"},   // project wins over agent
		{"read", LoadTypeOptIn, "agent"},      // agent wins over manifest
		{"search", LoadTypeOptIn, "manifest"}, // manifest is last resort
		{"unknown", LoadTypeAuto, "default"},  // not in any layer → auto
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := resolver.Resolve(tt.tool)
			if got != tt.wantLT {
				t.Errorf("Resolve(%q) = %q, want %q", tt.tool, got, tt.wantLT)
			}

			gotLT, gotSource := resolver.ResolveWithSource(tt.tool)
			if gotLT != tt.wantLT || gotSource != tt.wantSource {
				t.Errorf("ResolveWithSource(%q) = (%q, %q), want (%q, %q)",
					tt.tool, gotLT, gotSource, tt.wantLT, tt.wantSource)
			}
		})
	}
}

func TestLoadTypeResolver_IsToolEnabled(t *testing.T) {
	resolver := NewLoadTypeResolver(
		LoadTypeLayer{Name: "user", Overrides: map[string]LoadType{
			"hidden_tool":  LoadTypeOptIn,
			"enabled_tool": LoadTypeAuto,
		}},
	)

	if resolver.IsToolEnabled("hidden_tool") {
		t.Error("hidden_tool should not be enabled")
	}
	if !resolver.IsToolEnabled("enabled_tool") {
		t.Error("enabled_tool should be enabled")
	}
	if !resolver.IsToolEnabled("unset_tool") {
		t.Error("unset_tool should default to enabled")
	}
}

func TestToolLoadPreferences_ToLayer(t *testing.T) {
	prefs := ToolLoadPreferences{
		"tool_a": LoadTypeOptIn,
		"tool_b": LoadTypeAuto,
	}

	layer := prefs.ToLayer("user")
	if layer.Name != "user" {
		t.Errorf("layer name = %q, want %q", layer.Name, "user")
	}
	if layer.Overrides["tool_a"] != LoadTypeOptIn {
		t.Errorf("tool_a = %q, want %q", layer.Overrides["tool_a"], LoadTypeOptIn)
	}
	if layer.Overrides["tool_b"] != LoadTypeAuto {
		t.Errorf("tool_b = %q, want %q", layer.Overrides["tool_b"], LoadTypeAuto)
	}
}
