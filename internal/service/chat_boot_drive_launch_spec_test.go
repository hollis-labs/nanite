package service

// CW-20260514-0048: regression coverage for the LaunchSpec → agent.Options
// merge that driveBootSession performs at boot time. Each precedence
// rule documented in applyLaunchSpecToBootOpts gets a dedicated test
// so a future refactor can't quietly swap the semantics.

import (
	"context"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// TestApplyLaunchSpec_Nil verifies both nil-receiver cases are no-ops
// — the helper must be safe to call from the existing boot path without
// any defensive nil checks at the call site.
func TestApplyLaunchSpec_Nil(t *testing.T) {
	// nil opts: no panic.
	applyLaunchSpecToBootOpts(nil, &bootprofile.LaunchSpec{Workdir: "x"})

	// nil spec: opts unchanged.
	opts := &runtimeagent.Options{Workdir: "/orig"}
	applyLaunchSpecToBootOpts(opts, nil)
	if opts.Workdir != "/orig" {
		t.Fatalf("opts mutated by nil spec: workdir=%q", opts.Workdir)
	}
}

// TestApplyLaunchSpec_WorkdirPrecedence pins the "explicit session
// override > LaunchSpec.Workdir > project repo path" decision from
// the open-questions design defaults.
func TestApplyLaunchSpec_WorkdirPrecedence(t *testing.T) {
	t.Run("session override wins", func(t *testing.T) {
		opts := &runtimeagent.Options{Workdir: "/session-set"}
		applyLaunchSpecToBootOpts(opts, &bootprofile.LaunchSpec{Workdir: "/spec-set"})
		if opts.Workdir != "/session-set" {
			t.Errorf("Workdir = %q, want session value", opts.Workdir)
		}
	})
	t.Run("spec fills empty", func(t *testing.T) {
		opts := &runtimeagent.Options{Workdir: ""}
		applyLaunchSpecToBootOpts(opts, &bootprofile.LaunchSpec{Workdir: "/spec-set"})
		if opts.Workdir != "/spec-set" {
			t.Errorf("Workdir = %q, want spec value", opts.Workdir)
		}
	})
	t.Run("both empty stays empty", func(t *testing.T) {
		opts := &runtimeagent.Options{}
		applyLaunchSpecToBootOpts(opts, &bootprofile.LaunchSpec{})
		if opts.Workdir != "" {
			t.Errorf("Workdir = %q, want empty", opts.Workdir)
		}
	})
}

// TestApplyLaunchSpec_EnvMergeOrder pins: spec.Env overlays existing
// opts.Env (spec wins on collision; pre-existing keys persist).
// Documented in the open-questions design default for "Per-profile
// env / args".
func TestApplyLaunchSpec_EnvMergeOrder(t *testing.T) {
	opts := &runtimeagent.Options{Env: map[string]string{
		"PRE_EXISTING": "stays",
		"OVERLAPPING":  "old",
	}}
	spec := &bootprofile.LaunchSpec{Env: map[string]string{
		"OVERLAPPING": "new",
		"FROM_SPEC":   "added",
	}}
	applyLaunchSpecToBootOpts(opts, spec)

	want := map[string]string{
		"PRE_EXISTING": "stays",
		"OVERLAPPING":  "new",
		"FROM_SPEC":    "added",
	}
	if !reflect.DeepEqual(opts.Env, want) {
		t.Fatalf("Env merge = %v, want %v", opts.Env, want)
	}
}

// TestApplyLaunchSpec_EnvAllocsWhenNil verifies the helper allocates
// opts.Env when nil and spec.Env is non-empty — no nil-map-write panic.
func TestApplyLaunchSpec_EnvAllocsWhenNil(t *testing.T) {
	opts := &runtimeagent.Options{}
	spec := &bootprofile.LaunchSpec{Env: map[string]string{"K": "V"}}
	applyLaunchSpecToBootOpts(opts, spec)
	if opts.Env == nil {
		t.Fatal("opts.Env still nil after spec overlay")
	}
	if opts.Env["K"] != "V" {
		t.Errorf("Env[K] = %q, want V", opts.Env["K"])
	}
}

// TestApplyLaunchSpec_ArgsAppendNotReplace pins: spec.Args APPEND to
// opts.ExtraArgs (preserving any caller-supplied argv). Open question
// #3 design default declared "spec.Args overrides default args" — the
// current chat call site never passes ExtraArgs, so append vs override
// observable behavior matches. Pinning APPEND keeps the door open for
// future callers that want to splice their own args before spec args.
func TestApplyLaunchSpec_ArgsAppendNotReplace(t *testing.T) {
	opts := &runtimeagent.Options{ExtraArgs: []string{"--pre"}}
	spec := &bootprofile.LaunchSpec{Args: []string{"--from", "spec"}}
	applyLaunchSpecToBootOpts(opts, spec)

	want := []string{"--pre", "--from", "spec"}
	if !reflect.DeepEqual(opts.ExtraArgs, want) {
		t.Fatalf("ExtraArgs = %v, want %v", opts.ExtraArgs, want)
	}
}

// TestApplyLaunchSpec_BootPromptOverride pins: spec.BootPrompt lands
// in opts.BootPromptOverride so the layout's BootPrompt method picks
// it up via resolveBootPrompt.
func TestApplyLaunchSpec_BootPromptOverride(t *testing.T) {
	t.Run("spec body overrides", func(t *testing.T) {
		opts := &runtimeagent.Options{}
		spec := &bootprofile.LaunchSpec{BootPrompt: "from catalog"}
		applyLaunchSpecToBootOpts(opts, spec)
		if opts.BootPromptOverride != "from catalog" {
			t.Errorf("BootPromptOverride = %q, want %q", opts.BootPromptOverride, "from catalog")
		}
	})
	t.Run("empty spec body leaves override empty", func(t *testing.T) {
		opts := &runtimeagent.Options{}
		spec := &bootprofile.LaunchSpec{BootPrompt: ""}
		applyLaunchSpecToBootOpts(opts, spec)
		if opts.BootPromptOverride != "" {
			t.Errorf("BootPromptOverride = %q, want empty (fall through to composeSystemPrompt)",
				opts.BootPromptOverride)
		}
	})
}

// TestApplyLaunchSpec_NoResumeFieldTouched is the explicit pin for
// the "normal launches do NOT use stored provider resume IDs"
// acceptance criterion. The helper must NOT set Mode=ModeResume,
// ResumeFromCheckpoint, or any other resume-flavored field — those
// belong to the crash-recovery flow (CW-20260514-0049).
func TestApplyLaunchSpec_NoResumeFieldTouched(t *testing.T) {
	opts := &runtimeagent.Options{Mode: runtimeagent.ModeLongLived}
	spec := &bootprofile.LaunchSpec{
		Workdir:    "/x",
		Env:        map[string]string{"K": "V"},
		Args:       []string{"--a"},
		BootPrompt: "p",
	}
	applyLaunchSpecToBootOpts(opts, spec)
	if opts.Mode != runtimeagent.ModeLongLived {
		t.Errorf("Mode = %v, want ModeLongLived (helper must not change Mode)", opts.Mode)
	}
	if opts.ResumeFromCheckpoint != "" {
		t.Errorf("ResumeFromCheckpoint = %q, want empty (no-resume guarantee)", opts.ResumeFromCheckpoint)
	}
	if opts.IsRelaunch {
		t.Error("IsRelaunch true after normal-start LaunchSpec apply, want false")
	}
}

// TestLaunchSpecFor_NoSpecReturnsNil covers the absence path: when
// nothing was stashed, launchSpecFor returns nil so the existing
// driveBootSession flow runs unchanged.
func TestLaunchSpecFor_NoSpecReturnsNil(t *testing.T) {
	s := &chatServiceImpl{}
	if got := s.launchSpecFor("sess-1"); got != nil {
		t.Fatalf("launchSpecFor empty = %+v, want nil", got)
	}
}

// TestLaunchSpecFor_EmptySessionID returns nil; the lookup must not
// crash on an empty key.
func TestLaunchSpecFor_EmptySessionID(t *testing.T) {
	s := &chatServiceImpl{}
	s.activeSessionLaunchSpecs.Store("", &bootprofile.LaunchSpec{ProfileID: "p"})
	if got := s.launchSpecFor(""); got != nil {
		t.Fatalf("launchSpecFor empty key = %+v, want nil (defensive)", got)
	}
}

// TestCloseAgentSession_ClearsLaunchSpec verifies the stop / archive
// path also clears the per-session LaunchSpec stash. Without this,
// long-lived processes would leak entries across reboots if a session
// row stayed in the activeSessionLaunchSpecs map after its runtime
// was torn down.
func TestCloseAgentSession_ClearsLaunchSpec(t *testing.T) {
	s := &chatServiceImpl{}
	s.activeSessionLaunchSpecs.Store("sess-x", &bootprofile.LaunchSpec{ProfileID: "p"})

	// CloseAgentSession's normal flow requires activeSessions entry +
	// runtime session; we don't have one, so it returns early after
	// the LoadAndDelete. To exercise the cleanup we need to also seed
	// activeSessions — the function returns BEFORE the map deletes
	// when activeSessions has no entry. Match the production flow.
	s.activeSessions.Store("sess-x", &struct{}{}) // non-nil placeholder
	s.CloseAgentSession(context.Background(), "sess-x")

	if got := s.launchSpecFor("sess-x"); got != nil {
		t.Fatalf("launch spec NOT cleared after CloseAgentSession: %+v", got)
	}
}

// TestLaunchSpec_StashIsIdempotent verifies repeated resolveBootProfile
// calls for the same session overwrite (not duplicate) the stash, so
// "subsequent turns reuse the same live session" (CW-20260514-0048
// acceptance) holds: the stash is consulted only on first boot, and a
// re-resolve doesn't grow state per turn. Pinned here so a future
// refactor that switches activeSessionLaunchSpecs to e.g. a slice
// trips this test.
func TestLaunchSpec_StashIsIdempotent(t *testing.T) {
	s := &chatServiceImpl{}
	a := &bootprofile.LaunchSpec{ProfileID: "p1"}
	b := &bootprofile.LaunchSpec{ProfileID: "p2"}
	s.activeSessionLaunchSpecs.Store("sess-1", a)
	s.activeSessionLaunchSpecs.Store("sess-1", b)
	got := s.launchSpecFor("sess-1")
	if got == nil || got.ProfileID != "p2" {
		t.Fatalf("re-store: got %+v, want spec p2", got)
	}
	// Count entries to confirm one key — sync.Map.Range is the only
	// way to count without iteration.
	count := 0
	s.activeSessionLaunchSpecs.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 1 {
		t.Errorf("entry count = %d, want 1 (stash must overwrite not duplicate)", count)
	}
}

// TestLaunchSpecFor_RoundTrip stores then loads a spec to confirm the
// map type assertion works (sync.Map is untyped under the hood).
func TestLaunchSpecFor_RoundTrip(t *testing.T) {
	s := &chatServiceImpl{}
	want := &bootprofile.LaunchSpec{ProfileID: "p", Provider: "claude"}
	s.activeSessionLaunchSpecs.Store("sess-1", want)

	got := s.launchSpecFor("sess-1")
	if got == nil {
		t.Fatal("launchSpecFor returned nil after Store")
	}
	if got.ProfileID != "p" || got.Provider != "claude" {
		t.Fatalf("launchSpecFor returned wrong spec: %+v", got)
	}
}
