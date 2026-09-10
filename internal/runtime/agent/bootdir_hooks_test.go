package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

func hookTestParams(hooks []BootDirHook) SetupParams {
	return SetupParams{
		SessionID:    "sess-hooks",
		AgentProfile: &store.AgentProfile{Name: "Hooked", Slug: "hooked"},
		SystemPrompt: "be helpful",
		BootContent:  "# kickoff\n",
		Hooks:        hooks,
	}
}

// TestDefaultBootDirHooks_IsEmpty pins the mechanism/policy split.
// CW-20260910-0015 builds the plumbing; CW-20260910-0016 decides what
// ships. If this ever fails, a plumbing change has shipped an opinion —
// and a hook planted by default fires for EVERY agent Nanite boots.
func TestDefaultBootDirHooks_IsEmpty(t *testing.T) {
	if len(DefaultBootDirHooks) != 0 {
		t.Fatalf("DefaultBootDirHooks = %v, want empty — hook policy is CW-20260910-0016, not this file", DefaultBootDirHooks)
	}
}

// TestClaudeLayout_PlantsHookScriptAndDeclaration is the end-to-end
// assertion that a planted hook can actually fire: the script exists and
// is executable, AND the settings document declares it. Either half alone
// is inert.
func TestClaudeLayout_PlantsHookScriptAndDeclaration(t *testing.T) {
	bootDir := t.TempDir()
	hooks := []BootDirHook{{
		Event:  HookStop,
		Name:   "stop-disposition.sh",
		Script: []byte("#!/usr/bin/env bash\nexit 0\n"),
	}}
	if _, err := (claudeLayout{}).Populate(bootDir, hookTestParams(hooks)); err != nil {
		t.Fatalf("Populate with hooks: %v", err)
	}

	script := filepath.Join(bootDir, "hooks/claude/stop-disposition.sh")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("hook script not planted: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("hook script mode = %o, want 0700 — a non-executable hook cannot run", perm)
	}
	dir, err := os.Stat(filepath.Join(bootDir, "hooks/claude"))
	if err != nil || !dir.IsDir() {
		t.Fatalf("hook dir not planted: %v", err)
	}

	settings := readClaudeSettings(t, bootDir)
	hooksVal, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("settings.json carries no hooks declaration: %v", settings)
	}
	stop, ok := hooksVal["Stop"].([]any)
	if !ok || len(stop) != 1 {
		t.Fatalf("Stop declaration = %v, want one entry", hooksVal["Stop"])
	}
	cmd := firstHookCommand(t, stop[0])
	if cmd != script {
		t.Errorf("declared command = %q, want the planted script's absolute path %q", cmd, script)
	}
	if !filepath.IsAbs(cmd) {
		t.Errorf("declared command %q is not absolute", cmd)
	}
}

// TestClaudeHookSettings_TwoHooksOnOneEventStaySeparate pins the design
// detail carried over from agent-setup: stop-disposition.sh and
// context-report.sh are two Stop entries because "that hook speaks to the
// agent at exit 2 and this one reports to the operator at exit 0. Same
// event, opposite audiences." Merging them would make one of them wrong.
func TestClaudeHookSettings_TwoHooksOnOneEventStaySeparate(t *testing.T) {
	hooks := []BootDirHook{
		{Event: HookStop, Name: "stop-disposition.sh", Script: []byte("exit 0\n")},
		{Event: HookStop, Name: "context-report.sh", Script: []byte("exit 0\n")},
	}
	got, err := claudeHookSettings("/boot", hooks)
	if err != nil {
		t.Fatalf("claudeHookSettings: %v", err)
	}
	stop, _ := got["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop entries = %d, want 2 separate declarations", len(stop))
	}
	first := firstHookCommand(t, stop[0])
	second := firstHookCommand(t, stop[1])
	if !strings.HasSuffix(first, "stop-disposition.sh") || !strings.HasSuffix(second, "context-report.sh") {
		t.Errorf("declaration order not preserved: %q then %q", first, second)
	}
}

// TestClaudeHookSettings_MatcherRules verifies a matcher reaches the
// tool events and is refused on the lifecycle events, where it would be
// silently meaningless.
func TestClaudeHookSettings_MatcherRules(t *testing.T) {
	got, err := claudeHookSettings("/boot", []BootDirHook{{
		Event: HookPreToolUse, Matcher: "Bash", Name: "guard.sh", Script: []byte("exit 0\n"),
	}})
	if err != nil {
		t.Fatalf("PreToolUse with matcher: %v", err)
	}
	entries, _ := got["PreToolUse"].([]any)
	entry, _ := entries[0].(map[string]any)
	if entry["matcher"] != "Bash" {
		t.Errorf("matcher = %v, want Bash", entry["matcher"])
	}

	if _, err := claudeHookSettings("/boot", []BootDirHook{{
		Event: HookStop, Matcher: "Bash", Name: "x.sh", Script: []byte("exit 0\n"),
	}}); err == nil {
		t.Error("expected a matcher on Stop to be rejected")
	}
}

// TestClaudeSettings_UnchangedWithoutHooks is the regression that keeps
// this feature free: with no hooks, the planted settings bytes must be
// exactly what the go-providers render path produced before hook support
// existed.
func TestClaudeSettings_UnchangedWithoutHooks(t *testing.T) {
	adapter := provider.NewClaudeAdapter()
	adapter.PermissionMode = claudeDefaultPermissionMode
	viaRender, err := renderProviderConfigFile(adapter, ".claude/settings.json", provider.PlantContext{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	viaContent, err := claudeProviderConfigContent(nil, nil)
	if err != nil {
		t.Fatalf("claudeProviderConfigContent: %v", err)
	}
	if viaContent != viaRender {
		t.Errorf("no-hook settings drifted from the render path:\n got %q\nwant %q", viaContent, viaRender)
	}
	if strings.Contains(viaContent, "hooks") {
		t.Errorf("no-hook settings mention hooks: %q", viaContent)
	}
}

// TestHooks_RejectedForUnwiredProviders verifies codex and opencode fail
// loudly rather than planting scripts nothing declares.
func TestHooks_RejectedForUnwiredProviders(t *testing.T) {
	hooks := []BootDirHook{{Event: HookStop, Name: "x.sh", Script: []byte("exit 0\n")}}
	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"codex", func() error { _, err := codexPlantSpec(hookTestParams(hooks)); return err }},
		{"opencode", func() error { _, err := opencodePlantSpec(hookTestParams(hooks)); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if err == nil {
				t.Fatal("expected hooks to be rejected for this provider")
			}
			if !strings.Contains(err.Error(), "not supported") {
				t.Errorf("error should say hooks are unsupported, got %v", err)
			}
		})
	}
}

// TestValidateHooks_Rejections covers the shapes that would plant a
// broken or unsafe hook.
func TestValidateHooks_Rejections(t *testing.T) {
	cases := map[string]BootDirHook{
		"unknown event":  {Event: "Whenever", Name: "x.sh", Script: []byte("x")},
		"empty name":     {Event: HookStop, Name: "", Script: []byte("x")},
		"path separator": {Event: HookStop, Name: "../escape.sh", Script: []byte("x")},
		"empty script":   {Event: HookStop, Name: "x.sh"},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateHooks([]BootDirHook{h}); err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
		})
	}
	dup := []BootDirHook{
		{Event: HookStop, Name: "x.sh", Script: []byte("a")},
		{Event: HookPreToolUse, Name: "x.sh", Script: []byte("b")},
	}
	if err := validateHooks(dup); err == nil {
		t.Error("expected duplicate hook names to be rejected")
	}
}

// TestHookArtifactEntries_EmptySetPlantsNothing verifies a no-hook boot
// leaves no empty hooks/ directory suggesting an armed mechanism.
func TestHookArtifactEntries_EmptySetPlantsNothing(t *testing.T) {
	entries, err := hookArtifactEntries("claude", nil)
	if err != nil {
		t.Fatalf("hookArtifactEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want none for an empty hook set", entries)
	}
	bootDir := t.TempDir()
	if _, err := (claudeLayout{}).Populate(bootDir, hookTestParams(nil)); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bootDir, "hooks")); !os.IsNotExist(err) {
		t.Errorf("hooks/ exists after a no-hook boot (err=%v)", err)
	}
}

// TestClaudeLayout_HookSurvivesSlotRegeneration verifies a planted hook
// is still there after RegenerateSystemPromptSlot — the watchdog
// remediation path rewrites only CLAUDE.md, and a gate that vanished on
// remediation would be worse than no gate.
func TestClaudeLayout_HookSurvivesSlotRegeneration(t *testing.T) {
	bootDir := t.TempDir()
	params := hookTestParams([]BootDirHook{{
		Event: HookStop, Name: "stop.sh", Script: []byte("exit 0\n"),
	}})
	if _, err := (claudeLayout{}).Populate(bootDir, params); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if err := (claudeLayout{}).RegenerateSystemPromptSlot(bootDir, params); err != nil {
		t.Fatalf("RegenerateSystemPromptSlot: %v", err)
	}
	info, err := os.Stat(filepath.Join(bootDir, "hooks/claude/stop.sh"))
	if err != nil {
		t.Fatalf("hook script gone after slot regeneration: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("hook script mode = %o after regeneration, want 0700", perm)
	}
	if _, ok := readClaudeSettings(t, bootDir)["hooks"]; !ok {
		t.Error("hook declaration gone from settings after slot regeneration")
	}
}

func readClaudeSettings(t *testing.T, bootDir string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(bootDir, ".claude/settings.json")) //nolint:gosec // reads a file this test just planted into t.TempDir()
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v (%s)", err, body)
	}
	return doc
}

func firstHookCommand(t *testing.T, entry any) string {
	t.Helper()
	m, ok := entry.(map[string]any)
	if !ok {
		t.Fatalf("hook entry is not an object: %v", entry)
	}
	list, ok := m["hooks"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("hook entry carries no hooks list: %v", m)
	}
	inner, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("hook command is not an object: %v", list[0])
	}
	if inner["type"] != "command" {
		t.Errorf("hook type = %v, want command", inner["type"])
	}
	cmd, _ := inner["command"].(string)
	return cmd
}
