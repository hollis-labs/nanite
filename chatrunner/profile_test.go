package chatrunner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chat "github.com/hollis-labs/libs/agent/chat"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/chatrunner"
)

func runnerProfile(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	raw := "---\nschema_version: \"2\"\ndefinition_id: def:chat-profile\nrevision: \"1\"\nname: chat-profile\ndescription: Chat profile.\nbehavior:\n  purpose: Test.\nrequirements: {}\nharness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: durable\n---\n" + text
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestTurnProfileHookAndRecovery(t *testing.T) {
	profile := runnerProfile(t, "PROFILE FROM ORIGINAL TURN")
	t.Setenv("NANITE_RESUME_PROFILE", profile)
	dir := crashedDir(t)
	hookCalls := 0
	w := newWiredIn(t, dir, false, func(o *chatrunner.Options) {
		o.ResumeInterrupted = true
		o.TurnProfile = func(context.Context, chat.Turn) (string, map[string]string, error) {
			hookCalls++
			return "", nil, errors.New("new turns refused")
		}
	})
	if _, err := w.runner.RecoverTurns(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !w.answered(t, "resumed profile turn") {
		t.Fatal("turn did not resume")
	}
	req := w.brain.request()
	var prompt strings.Builder
	for _, slot := range req.SlotBlocks {
		prompt.WriteString(slot.Content)
	}
	if !strings.Contains(prompt.String(), "PROFILE FROM ORIGINAL TURN") {
		t.Fatal("recovery lost original profile")
	}
	if hookCalls != 0 {
		t.Fatal("recovery called current profile hook")
	}
	if _, err := w.runner.Start(t.Context(), chat.Turn{ConversationID: w.conv, Text: "new"}); err == nil || !strings.Contains(err.Error(), "new turns refused") {
		t.Fatalf("hook error: %v", err)
	}
}
func TestTurnProfileHookSwitchesWithoutRestaging(t *testing.T) {
	a, b := runnerProfile(t, "PROFILE ALPHA"), runnerProfile(t, "PROFILE BETA")
	current := a
	w := newWiredOpts(t, false, nil, func(o *chatrunner.Options) {
		o.TurnProfile = func(context.Context, chat.Turn) (string, map[string]string, error) { return current, nil, nil }
	})
	for _, dir := range []string{a, b} {
		current = dir
		if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hello"}); err != nil {
			t.Fatal(err)
		}
		w.until(t, "profile turn", func(ev chatstream.Event) bool { return ev.IsTerminal() })
		want := "PROFILE ALPHA"
		if dir == b {
			want = "PROFILE BETA"
		}
		var prompt strings.Builder
		for _, slot := range w.brain.request().SlotBlocks {
			prompt.WriteString(slot.Content)
		}
		if !strings.Contains(prompt.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestRecoveryRefusesMissingPersistedProfile(t *testing.T) {
	profile := runnerProfile(t, "original")
	t.Setenv("NANITE_RESUME_PROFILE", profile)
	dir := crashedDir(t)
	moved := profile + "-moved"
	if err := os.Rename(profile, moved); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(moved, profile) }()
	w := newWiredIn(t, dir, false, func(o *chatrunner.Options) { o.ResumeInterrupted = true })
	if _, err := w.runner.RecoverTurns(t.Context()); err != nil {
		t.Fatal(err)
	}
	events := w.until(t, "failed profile recovery", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	found := false
	for _, ev := range events {
		if ev.Verb == chatstream.VerbRunError && strings.Contains(ev.Message, "turn profile") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing profile error: %+v", events)
	}
	if w.brain.answers(resumeScript) != 0 {
		t.Fatal("model ran on default profile")
	}
}
