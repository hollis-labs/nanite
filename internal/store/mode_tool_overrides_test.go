package store

import (
	"reflect"
	"testing"
)

func TestApplyToolOverrides_EmptySpecPassthrough(t *testing.T) {
	tools := []string{"fs_read", "fs_write", "shell_exec"}
	got := ApplyToolOverrides(tools, ToolOverrideSpec{})
	if !reflect.DeepEqual(got, tools) {
		t.Errorf("empty spec should passthrough; got %v want %v", got, tools)
	}
}

func TestApplyToolOverrides_DenyWinsOverAllow(t *testing.T) {
	// Tool in both Allow and Deny → Deny wins.
	tools := []string{"fs_read", "fs_write"}
	spec := ToolOverrideSpec{
		Allow: []string{"fs_read", "fs_write"},
		Deny:  []string{"fs_write"},
	}
	got := ApplyToolOverrides(tools, spec)
	want := []string{"fs_read"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deny should win over allow; got %v want %v", got, want)
	}
}

func TestApplyToolOverrides_ExplicitAllowBeatsDenyPattern(t *testing.T) {
	// Allow=["fs_read"] keeps fs_read even though DenyPatterns=["fs_*"]
	// would otherwise drop it.
	tools := []string{"fs_read", "fs_write", "fs_delete", "shell_exec"}
	spec := ToolOverrideSpec{
		Allow:        []string{"fs_read"},
		DenyPatterns: []string{"fs_*"},
	}
	got := ApplyToolOverrides(tools, spec)
	want := []string{"fs_read", "shell_exec"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("explicit Allow should beat DenyPattern; got %v want %v", got, want)
	}
}

func TestApplyToolOverrides_AllowPatternsWhitelist(t *testing.T) {
	// Non-empty AllowPatterns → only matching tools survive.
	tools := []string{"fs_read", "fs_write", "shell_exec", "net_fetch"}
	spec := ToolOverrideSpec{
		AllowPatterns: []string{"fs_*"},
	}
	got := ApplyToolOverrides(tools, spec)
	want := []string{"fs_read", "fs_write"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AllowPatterns should whitelist; got %v want %v", got, want)
	}
}

func TestApplyToolOverrides_MixedAllowAndDenyPatterns(t *testing.T) {
	// AllowPatterns=["fs_*", "shell_*"], DenyPatterns=["fs_delete"]
	tools := []string{"fs_read", "fs_delete", "shell_exec", "net_fetch"}
	spec := ToolOverrideSpec{
		AllowPatterns: []string{"fs_*", "shell_*"},
		DenyPatterns:  []string{"fs_delete"},
	}
	got := ApplyToolOverrides(tools, spec)
	want := []string{"fs_read", "shell_exec"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mixed allow/deny patterns; got %v want %v", got, want)
	}
}

func TestApplyToolOverrides_DenyOnly(t *testing.T) {
	// Deny without Allow/AllowPatterns: all non-denied pass through.
	tools := []string{"fs_read", "fs_write", "shell_exec"}
	spec := ToolOverrideSpec{
		Deny: []string{"shell_exec"},
	}
	got := ApplyToolOverrides(tools, spec)
	want := []string{"fs_read", "fs_write"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deny-only; got %v want %v", got, want)
	}
}

func TestParseToolOverrides_Empty(t *testing.T) {
	for _, raw := range []string{"", "{}"} {
		spec, err := ParseToolOverrides(raw)
		if err != nil {
			t.Errorf("ParseToolOverrides(%q) err: %v", raw, err)
		}
		if !isEmptyToolOverrideSpec(spec) {
			t.Errorf("ParseToolOverrides(%q) should be empty; got %+v", raw, spec)
		}
	}
}

func TestParseToolOverrides_FullShape(t *testing.T) {
	raw := `{"allow":["fs_read"],"deny":["shell_exec"],"allow_patterns":["dev_*"],"deny_patterns":["fs_delete"]}`
	spec, err := ParseToolOverrides(raw)
	if err != nil {
		t.Fatalf("ParseToolOverrides: %v", err)
	}
	if got, want := spec.Allow, []string{"fs_read"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Allow; got %v want %v", got, want)
	}
	if got, want := spec.Deny, []string{"shell_exec"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Deny; got %v want %v", got, want)
	}
	if got, want := spec.AllowPatterns, []string{"dev_*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AllowPatterns; got %v want %v", got, want)
	}
	if got, want := spec.DenyPatterns, []string{"fs_delete"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DenyPatterns; got %v want %v", got, want)
	}
}

func TestParseToolOverrides_InvalidJSON(t *testing.T) {
	_, err := ParseToolOverrides(`{not json`)
	if err == nil {
		t.Error("ParseToolOverrides should fail on invalid JSON")
	}
}
