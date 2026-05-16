package main

import (
	"slices"
	"testing"
)

// TestSplitLaunchArgs pins the flag-parsing fix (Copilot round-1 #1):
// the positional <profile-id> is separated out of the arg list FIRST so
// flags are honored regardless of their position relative to it. The
// stdlib flag package stops at the first non-flag arg, which is the bug
// this helper works around — `nanite launch claude-smoke --dry-run` must
// still honor --dry-run.
func TestSplitLaunchArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantProfile string
		wantFlags   []string
		wantErr     bool
	}{
		{
			name:        "flags before profile",
			args:        []string{"--dry-run", "claude-smoke"},
			wantProfile: "claude-smoke",
			wantFlags:   []string{"--dry-run"},
		},
		{
			name:        "flags after profile (the bug case)",
			args:        []string{"claude-smoke", "--dry-run"},
			wantProfile: "claude-smoke",
			wantFlags:   []string{"--dry-run"},
		},
		{
			name:        "value flag space form after profile",
			args:        []string{"claude-smoke", "--catalog", "/tmp/cat"},
			wantProfile: "claude-smoke",
			wantFlags:   []string{"--catalog", "/tmp/cat"},
		},
		{
			name:        "value flag eq form before profile",
			args:        []string{"--catalog=/tmp/cat", "claude-smoke"},
			wantProfile: "claude-smoke",
			wantFlags:   []string{"--catalog=/tmp/cat"},
		},
		{
			name:        "profile sandwiched between flags",
			args:        []string{"--catalog", "/tmp/cat", "claude-smoke", "--dry-run"},
			wantProfile: "claude-smoke",
			wantFlags:   []string{"--catalog", "/tmp/cat", "--dry-run"},
		},
		{
			name:        "single dash short flag form",
			args:        []string{"-dry-run", "claude-smoke"},
			wantProfile: "claude-smoke",
			wantFlags:   []string{"-dry-run"},
		},
		{
			name:        "double-dash terminator",
			args:        []string{"--dry-run", "--", "claude-smoke"},
			wantProfile: "claude-smoke",
			wantFlags:   []string{"--dry-run"},
		},
		{
			name:    "missing profile",
			args:    []string{"--dry-run"},
			wantErr: true,
		},
		{
			name:    "two positionals rejected",
			args:    []string{"claude-smoke", "extra-profile"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile, flags, err := splitLaunchArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitLaunchArgs(%v): expected error, got profile=%q flags=%v", tc.args, profile, flags)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitLaunchArgs(%v): unexpected error: %v", tc.args, err)
			}
			if profile != tc.wantProfile {
				t.Errorf("profile = %q, want %q", profile, tc.wantProfile)
			}
			if !slices.Equal(flags, tc.wantFlags) {
				t.Errorf("flags = %v, want %v", flags, tc.wantFlags)
			}
		})
	}
}
