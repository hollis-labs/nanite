package install

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func ptr(s []string) *[]string { return &s }

func TestResolveAdapters_NoAdaptersFlagWins(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{Adapters: ptr([]string{"claude", "codex"})}
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{NoAdapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Errorf("resolved: got %v, want []", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"claude", "codex"}) {
		t.Errorf("previous: got %v, want [claude codex]", previous)
	}
}

func TestResolveAdapters_FlagWinsOverConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{Adapters: ptr([]string{"gemini"})}
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{Flag: "claude,codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"claude", "codex"}) {
		t.Errorf("resolved: got %v, want [claude codex]", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"gemini"}) {
		t.Errorf("previous: got %v, want [gemini]", previous)
	}
}

func TestResolveAdapters_FlagRejectsUnknownSlug(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{}
	_, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Flag: "frobnicate"})
	if err == nil {
		t.Fatal("expected error for unknown slug, got nil")
	}
}

func TestResolveAdapters_FlagRejectsNaniteNative(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{}
	_, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Flag: "nanite-native"})
	if err == nil {
		t.Fatal("expected error for nanite-native (not user-selectable)")
	}
}

func TestResolveAdapters_ConfigWinsWhenNotReconfigure(t *testing.T) {
	dir := t.TempDir()
	// Plant CLAUDE.md so detection would find it.
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{Adapters: ptr([]string{"gemini"})}
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"gemini"}) {
		t.Errorf("resolved: got %v, want [gemini]", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"gemini"}) {
		t.Errorf("previous: got %v, want [gemini]", previous)
	}
}

func TestResolveAdapters_DetectionFallback_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{} // Adapters nil, key absent
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Interactive: false})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(resolved)
	if !reflect.DeepEqual(resolved, []string{"claude", "codex"}) {
		t.Errorf("resolved: got %v, want [claude codex]", resolved)
	}
}

func TestResolveAdapters_DetectionFallback_NonInteractive_Empty(t *testing.T) {
	dir := t.TempDir()
	cfg := &projectConfig{}
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Interactive: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Errorf("resolved: got %v, want []", resolved)
	}
}

func TestResolveAdapters_ReconfigureBypassesConfig_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "GEMINI.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{Adapters: ptr([]string{"claude"})}
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{Reconfigure: true, Interactive: false})
	if err != nil {
		t.Fatal(err)
	}
	// Non-interactive --reconfigure: keep current per spec section 4 step 8
	// (interpreted as: with no prompt, current = cfg.Adapters; resolved = current)
	if !reflect.DeepEqual(resolved, []string{"claude"}) {
		t.Errorf("resolved: got %v, want [claude] (non-interactive --reconfigure is no-op)", resolved)
	}
}

func TestResolveAdapters_InteractivePrompt_Accept(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{}
	in := bytes.NewBufferString("y\n")
	var out bytes.Buffer
	resolved, _, err := ResolveAdapters(cfg, dir, ResolveOpts{
		Interactive: true,
		Stdin:       in,
		Stdout:      &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"claude"}) {
		t.Errorf("resolved: got %v, want [claude]", resolved)
	}
}

func TestResolveAdapters_InteractivePrompt_Reconfigure_KeepsCurrent(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "GEMINI.md"), []byte("# x"), 0o644)
	cfg := &projectConfig{Adapters: ptr([]string{"claude"})}
	in := bytes.NewBufferString("\n")
	var out bytes.Buffer
	resolved, previous, err := ResolveAdapters(cfg, dir, ResolveOpts{
		Reconfigure: true,
		Interactive: true,
		Stdin:       in,
		Stdout:      &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, []string{"claude"}) {
		t.Errorf("resolved: got %v, want current [claude]", resolved)
	}
	if !reflect.DeepEqual(previous, []string{"claude"}) {
		t.Errorf("previous: got %v, want [claude]", previous)
	}
}
