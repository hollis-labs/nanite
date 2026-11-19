package engine

import (
	"slices"
	"strings"
	"testing"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/subagent"
)

func TestSlotInvariantsAcrossRoles(t *testing.T) {
	e, _, _ := setupEngine(t, nil, "")
	e.cfg.WorkspaceRoots = []string{t.TempDir()}
	def, err := definition.Load(e.cfg.Dir, e.cfg.GlobalSkillsDir)
	if err != nil {
		t.Fatal(err)
	}
	pdp, err := permission.Rules("", []byte("permissions:\n  mode: plan\n"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := skills.NewCatalog(nil, []skills.Skill{
		{Name: "lookup", Description: "Find a note", Body: "Skill instructions loaded on demand"},
	})
	universal := make(map[string]string)
	for _, tt := range []struct {
		role, modeText string
		hostMode       bool
	}{
		{role: "chat"},
		{role: "chat", modeText: "Host-selected mode", hostMode: true},
		{role: "worker", modeText: "You are a worker subtask."},
		{role: "planner", modeText: "You are a planner subtask."},
	} {
		name := tt.role
		if tt.hostMode {
			name += "/host-mode"
		}
		t.Run(name, func(t *testing.T) {
			input := Input{Slots: []slots.Update{
				{Slot: slots.Context, Entry: slots.Entry{Owner: "host", Key: "context", Content: "Host context"}},
				{Slot: slots.System, Entry: slots.Entry{Owner: "host", Key: "system", Content: "Host instructions"}},
			}}
			if tt.hostMode {
				input.Slots = append(input.Slots, slots.Update{
					Slot: slots.Mode, Entry: slots.Entry{Owner: "host", Key: "mode", Content: tt.modeText},
				})
			}
			info := slots.TurnInfo{Role: tt.role, TurnID: "first"}
			rolePrompt := ""
			if !tt.hostMode {
				rolePrompt = subagent.Builtins()[tt.role].Prompt
			}
			state := e.cfg.Slots.Snapshot()
			first, err := e.assemble(t.Context(), input, info, def, state, nil, catalog.Listing(), rolePrompt, pdp.Mode(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []slots.Name
			content := make(map[slots.Name]string)
			for _, block := range first.Blocks {
				name := slots.Name(block.SlotName)
				names = append(names, name)
				content[name] = block.Content
			}
			want := []slots.Name{slots.Universal, slots.System, slots.Agent}
			if tt.modeText != "" {
				want = append(want, slots.Mode)
			}
			want = append(want, slots.Permissions, slots.Skills, slots.Context)
			if !slices.Equal(names, want) {
				t.Fatalf("slot order = %v, want %v", names, want)
			}
			if !strings.Contains(content[slots.Mode], tt.modeText) {
				t.Errorf("mode content = %q, want %q", content[slots.Mode], tt.modeText)
			}
			for _, want := range []string{"Permission mode: plan.", e.cfg.WorkspaceRoots[0], "Approved Bash commands"} {
				if !strings.Contains(content[slots.Permissions], want) {
					t.Errorf("permission content = %q, missing %q", content[slots.Permissions], want)
				}
			}
			if want := "- lookup: Find a note"; content[slots.Skills] != want {
				t.Errorf("skill listing = %q, want %q", content[slots.Skills], want)
			}
			info.TurnID = "second"
			second, err := e.assemble(t.Context(), input, info, def, state, first.Hashes, catalog.Listing(), rolePrompt, pdp.Mode(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			initial, next := first.Blocks[0], second.Blocks[0]
			if !strings.HasPrefix(initial.Content, basePrompt) {
				t.Errorf("universal block does not open with the base prompt: %q", initial.Content)
			}
			universal[name] = initial.Content
			if next.SlotName != string(slots.Universal) || initial.Content == "" ||
				initial.Content != next.Content || initial.CacheKey != next.CacheKey || next.Changed {
				t.Errorf("universal block changed across turns: first = %+v, next = %+v", initial, next)
			}
		})
	}
	// The base prompt opens every request's cacheable prefix, so it must not
	// vary by role or host mode either.
	for name, content := range universal {
		if content != universal["chat"] {
			t.Errorf("universal block for %s differs from chat", name)
		}
	}
}

func TestBasePromptFitsItsBudget(t *testing.T) {
	if got := (ctxpkg.DefaultEstimator{}).Estimate(basePrompt); got > universalBudget {
		t.Fatalf("base prompt is %d tokens, over the Universal budget of %d", got, universalBudget)
	}
}
