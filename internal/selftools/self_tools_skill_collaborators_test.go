package selftools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// The skill tools reach the catalog, grants and uninstall index through
// SelfToolsTransport's collaborator fields, not through Store. Each test
// below leaves the real store able to say yes and swaps one field for a fake
// that says no, so the answer shows which one the tool consulted.

type denyingGrants struct{}

func (denyingGrants) GetAgentKnownSkill(context.Context, string, string) (*store.AgentKnownSkill, error) {
	return nil, store.ErrAgentKnownSkillNotFound
}

type emptySkillIndex struct{}

func (emptySkillIndex) GetSkillBySlug(context.Context, string) (*store.Skill, error) { return nil, nil }
func (emptySkillIndex) CreateSkill(context.Context, *store.Skill) error              { return nil }
func (emptySkillIndex) UpdateSkill(context.Context, *store.Skill) error              { return nil }

type refusingUninstallIndex struct{}

func (refusingUninstallIndex) DeleteSkill(context.Context, string) error {
	return errors.New("fake uninstall index refused")
}

func TestCallSkillGet_UsesSkillGrantsField(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	sk := installSkillGetFixture(t, idx, vendor, writeSkillGetFixture(t, "skill-get-fake-grants"))
	agent := makeSkillGetTestAgent(t, idx, "skill-get-fake-grants-agent")
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID:             agent.ID,
		SkillName:           sk.Slug,
		ApprovedContentHash: sk.ContentHash,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
		CapabilitiesGranted: "{}",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	transport := newSkillGetTransport(idx, vendor)
	transport.SkillGrants = denyingGrants{}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)
	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": sk.Slug, "params": map[string]any{"who": "World"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "no grant row exists") {
		t.Fatalf("expected the fake grants to deny, got: %+v", res)
	}
}

func TestCallSkillGet_UsesSkillIndexField(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	sk := installSkillGetFixture(t, idx, vendor, writeSkillGetFixture(t, "skill-get-fake-index"))
	agent := makeSkillGetTestAgent(t, idx, "skill-get-fake-index-agent")

	transport := newSkillGetTransport(idx, vendor)
	transport.SkillIndex = emptySkillIndex{}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)
	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": sk.Slug})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not found in the skill catalog") {
		t.Fatalf("expected the fake index to report the skill missing, got: %+v", res)
	}
}

func TestCallSkillDelete_UsesSkillUninstallIndexField(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	sk := installSkillGetFixture(t, idx, vendor, writeSkillGetFixture(t, "skill-delete-fake-index"))

	transport := newSkillGetTransport(idx, vendor)
	transport.SkillUninstallIndex = refusingUninstallIndex{}
	res, err := transport.CallTool(context.Background(), "skill_delete", map[string]any{"slug": sk.Slug})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "fake uninstall index refused") {
		t.Fatalf("expected the fake uninstall index to refuse, got: %+v", res)
	}
	if got, err := idx.GetSkillBySlug(context.Background(), sk.Slug); err != nil || got == nil {
		t.Fatalf("skill row should survive a refused uninstall: row=%v err=%v", got, err)
	}
}

// A transport built without the constructor, with a Store but no skill
// fields, reports the unwired store rather than dereferencing nil.
func TestCallSkillGet_UnsetSkillFields_ClearError(t *testing.T) {
	transport := &SelfToolsTransport{SkillVendor: newSkillGetTestVendor(t)}
	res, err := transport.CallTool(context.Background(), "skill_get", map[string]any{"slug": "anything"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "no store configured") {
		t.Fatalf("expected a clear 'no store configured' error, got: %+v", res)
	}
}
