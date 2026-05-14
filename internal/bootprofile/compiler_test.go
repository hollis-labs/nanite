package bootprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

func newTestProfile() Profile {
	return Profile{
		ID:          "nanite.backend.main",
		DisplayName: "Nanite — Backend",
		Launch:      "nanite-claude",
		Identity: Identity{
			LineageAlias: "nanite.backend.main",
			ProfileID:    "nanite-backend",
			Role:         "backend",
			Project:      "nanite",
			WorkRoot:     "~/Projects-apps/nanite",
		},
		Slots: map[string]SlotSource{
			"agent": {Type: "text", Content: "Role: {{role}} on {{project}}."},
		},
		MCPServers: []string{"vanta"},
	}
}

func TestCompile_HappyPath(t *testing.T) {
	prof := newTestProfile()
	launch := &Launch{
		ID:       "nanite-claude",
		Provider: "pty-claude",
		Workdir:  "~/Projects-apps/nanite",
		UILabel:  "Nanite (Claude PTY)",
		Env:      map[string]string{"NANITE_FOO": "bar"},
		BootMode: "file",
	}
	spec, err := Compile(prof, launch, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if spec.ProfileID != "nanite.backend.main" {
		t.Fatalf("spec.ProfileID = %q", spec.ProfileID)
	}
	if spec.LaunchID != "nanite-claude" {
		t.Fatalf("spec.LaunchID = %q", spec.LaunchID)
	}
	if spec.Provider != "claude" {
		t.Fatalf("spec.Provider = %q, want normalized %q", spec.Provider, "claude")
	}
	if spec.ProviderAlias != "pty-claude" {
		t.Fatalf("spec.ProviderAlias = %q", spec.ProviderAlias)
	}
	if spec.UILabel != "Nanite (Claude PTY)" {
		t.Fatalf("UILabel = %q", spec.UILabel)
	}
	if spec.Workdir != "~/Projects-apps/nanite" {
		t.Fatalf("Workdir = %q", spec.Workdir)
	}
	if spec.BootMode != "file" {
		t.Fatalf("BootMode = %q", spec.BootMode)
	}
	if got := spec.Env["NANITE_FOO"]; got != "bar" {
		t.Fatalf("env passthrough lost: %q", got)
	}
	if len(spec.MCPServers) != 1 || spec.MCPServers[0] != "vanta" {
		t.Fatalf("MCPServers = %v", spec.MCPServers)
	}
	if got := spec.Slots["agent"]; got != "Role: backend on nanite." {
		t.Fatalf("agent slot = %q (variables not substituted?)", got)
	}
	if spec.Identity.LineageAlias != "nanite.backend.main" {
		t.Fatalf("Identity not propagated: %+v", spec.Identity)
	}
	if len(spec.Requirements) != 0 {
		t.Fatalf("expected no requirements, got %v", spec.Requirements)
	}
	if !strings.Contains(spec.BootPrompt, "Role: backend on nanite.") {
		t.Fatalf("BootPrompt missing rendered slot: %q", spec.BootPrompt)
	}
	if !strings.Contains(spec.BootPrompt, "nanite.backend.main") {
		t.Fatalf("BootPrompt missing identity line: %q", spec.BootPrompt)
	}
}

func TestCompile_ProviderAliasNormalization(t *testing.T) {
	cases := []struct {
		alias string
		want  string
	}{
		{"pty", "claude"},
		{"pty-claude", "claude"},
		{"pty-codex", "codex"},
		{"pty-opencode", "opencode"},
		{"sub-claude", "claude"},
		{"anthropic", "anthropic"},
		{"openai", "openai"},
	}
	for _, tc := range cases {
		t.Run(tc.alias, func(t *testing.T) {
			prof := newTestProfile()
			launch := &Launch{ID: "l", Provider: tc.alias}
			spec, err := Compile(prof, launch, nil, "")
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if spec.Provider != tc.want {
				t.Fatalf("alias %q normalized to %q, want %q", tc.alias, spec.Provider, tc.want)
			}
			if spec.ProviderAlias != tc.alias {
				t.Fatalf("ProviderAlias = %q, want preserved %q", spec.ProviderAlias, tc.alias)
			}
		})
	}
}

func TestCompile_DeferredSlotProducesRequirementAndEmptyBootPrompt(t *testing.T) {
	prof := newTestProfile()
	prof.Slots["recap"] = SlotSource{Type: "cmd", Run: "echo hi", Timeout: "5s"}
	spec, err := Compile(prof, nil, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(spec.Requirements) != 1 {
		t.Fatalf("expected 1 requirement, got %d: %+v", len(spec.Requirements), spec.Requirements)
	}
	if spec.Requirements[0].Slot != "recap" {
		t.Fatalf("requirement slot = %q", spec.Requirements[0].Slot)
	}
	if spec.BootPrompt != "" {
		t.Fatalf("BootPrompt should be empty when requirements remain, got %q", spec.BootPrompt)
	}
}

func TestCompile_UnknownVarErrors(t *testing.T) {
	prof := newTestProfile()
	prof.Slots["agent"] = SlotSource{Type: "text", Content: "hi {{nope}}"}
	_, err := Compile(prof, nil, nil, "")
	if err == nil {
		t.Fatal("expected unknown-var error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error %q should mention missing var", err)
	}
}

func TestCompile_CallerVarsOverrideProfileVars(t *testing.T) {
	prof := newTestProfile()
	prof.Vars = map[string]string{"shared": "from-profile"}
	prof.Slots["agent"] = SlotSource{Type: "text", Content: "v={{shared}}"}
	spec, err := Compile(prof, nil, Vars{"shared": "from-caller"}, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if got := spec.Slots["agent"]; got != "v=from-caller" {
		t.Fatalf("got %q, want caller value to win", got)
	}
}

func TestCompile_MissingIdentityIsAnError(t *testing.T) {
	_, err := Compile(Profile{ID: "x"}, nil, nil, "")
	if err == nil {
		t.Fatal("expected identity-required error")
	}
	if !strings.Contains(err.Error(), "lineage_alias") {
		t.Fatalf("error %q should call out the missing field", err)
	}
}

func TestCompile_EmptyProfileIDIsAnError(t *testing.T) {
	_, err := Compile(Profile{}, nil, nil, "")
	if err == nil {
		t.Fatal("expected empty-id error")
	}
}

func TestCompile_UILabelFallback(t *testing.T) {
	prof := Profile{
		ID:       "x.y.z",
		Identity: Identity{LineageAlias: "x.y.z"},
	}
	// No DisplayName, no Launch → falls back to ID.
	spec, err := Compile(prof, nil, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if spec.UILabel != "x.y.z" {
		t.Fatalf("UILabel = %q, want id fallback", spec.UILabel)
	}
	prof.DisplayName = "X Y Z"
	spec, _ = Compile(prof, nil, nil, "")
	if spec.UILabel != "X Y Z" {
		t.Fatalf("UILabel = %q, want display_name fallback", spec.UILabel)
	}
	// Launch label wins over DisplayName.
	spec, _ = Compile(prof, &Launch{ID: "l", Provider: "anthropic", UILabel: "L Label"}, nil, "")
	if spec.UILabel != "L Label" {
		t.Fatalf("UILabel = %q, want launch override", spec.UILabel)
	}
}

func TestCompile_LaunchSpecRoundtripsJSON(t *testing.T) {
	prof := newTestProfile()
	launch := &Launch{ID: "nanite-claude", Provider: "pty-claude"}
	spec, err := Compile(prof, launch, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Ensure all required fields are populated and the struct is JSON-clean.
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var roundtrip LaunchSpec
	if err := json.Unmarshal(b, &roundtrip); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if roundtrip.Provider != "claude" {
		t.Fatalf("roundtripped Provider = %q", roundtrip.Provider)
	}
	if roundtrip.ProfileID != prof.ID {
		t.Fatalf("roundtripped ProfileID = %q", roundtrip.ProfileID)
	}
}

func TestCompileFromCatalog_HappyPath(t *testing.T) {
	prof := newTestProfile()
	launch := Launch{ID: "nanite-claude", Provider: "pty-claude"}
	cat := &Catalog{
		Profiles: map[string]Profile{prof.ID: prof},
		Launches: map[string]Launch{launch.ID: launch},
	}
	spec, err := CompileFromCatalog(cat, prof.ID, nil)
	if err != nil {
		t.Fatalf("CompileFromCatalog: %v", err)
	}
	if spec.Provider != "claude" {
		t.Fatalf("spec.Provider = %q", spec.Provider)
	}
	if spec.LaunchID != "nanite-claude" {
		t.Fatalf("spec.LaunchID = %q", spec.LaunchID)
	}
}

func TestCompile_IdentityVarsAvailable(t *testing.T) {
	prof := newTestProfile()
	prof.Slots["agent"] = SlotSource{
		Type:    "text",
		Content: "alias={{lineage_alias}} dotted={{identity.work_root}}",
	}
	spec, err := Compile(prof, nil, nil, "")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got := spec.Slots["agent"]
	if !strings.Contains(got, "alias=nanite.backend.main") {
		t.Fatalf("flat identity key missing: %q", got)
	}
	if !strings.Contains(got, "dotted=~/Projects-apps/nanite") {
		t.Fatalf("dotted identity key missing: %q", got)
	}
}
