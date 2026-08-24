package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/workspace"
)

// mkfs is a tiny helper for these tests — mirrors workspace/walkup_test.go's
// mkfile but local to keep packages independent.
func mkfs(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writefile %s: %v", path, err)
	}
}

func mkgitDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkgit: %v", err)
	}
}

// TestAssembleSlots_WorkspaceSlotShipsWalkUpContent is the
// CW-20260512-0116 end-to-end smoke test: chat.AssembleSlotSources runs
// the AGENTS.md walk-up via WorkspaceCache+WorkingDirForSession and the
// Context Broker ships the result at the SlotWorkspace position in the
// assembled SlotBlocks. The session's resolved working_dir contains an
// AGENTS.md whose body must appear verbatim in the workspace block.
func TestAssembleSlots_WorkspaceSlotShipsWalkUpContent(t *testing.T) {
	// On-disk fixture: a fake repo with an AGENTS.md at the root.
	repoRoot := t.TempDir()
	mkgitDir(t, repoRoot)
	mkfs(t, filepath.Join(repoRoot, "AGENTS.md"), "PROJECT-SPECIFIC-RULES")

	s, err := storetest.New(t, context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sess := &store.Session{ID: "ws-sess"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	client := chat.NewContextClient(s)
	client.WorkspaceCache = workspace.NewCache()
	client.WorkingDirForSession = func(*store.Session) (string, error) {
		return repoRoot, nil
	}
	svc := NewContextService(ContextServiceConfig{Client: client})

	agent := &store.AgentProfile{ID: "ws-agent", Slug: "ws", Status: "active", SystemPrompt: "p"}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	var wsBlock ctxpkg.SlotBlock
	var found bool
	for _, b := range result.Blocks {
		if b.SlotName == ctxpkg.SlotWorkspace {
			wsBlock = b
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SlotWorkspace not present in assembled blocks; got %v",
			func() []string {
				names := make([]string, 0, len(result.Blocks))
				for _, b := range result.Blocks {
					names = append(names, b.SlotName)
				}
				return names
			}())
	}
	if !strings.Contains(wsBlock.Content, "PROJECT-SPECIFIC-RULES") {
		t.Errorf("workspace block missing AGENTS.md content; got %q", wsBlock.Content)
	}
	if !strings.Contains(wsBlock.Content, "Instructions from: "+filepath.Join(repoRoot, "AGENTS.md")) {
		t.Errorf("workspace block missing opencode-style header; got %q", wsBlock.Content)
	}
}

// TestAssembleSlots_WorkspaceSlotEmptyWithoutCache verifies the
// nil-WorkspaceCache path: the slot ships empty and the assembly decider
// emits it with empty content (skipped_no_content). This is the
// graceful-degrade contract — failing to wire the cache should never
// break the assembly pipeline.
func TestAssembleSlots_WorkspaceSlotEmptyWithoutCache(t *testing.T) {
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sess := &store.Session{ID: "ws-nil-sess"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Deliberately leave WorkspaceCache and WorkingDirForSession unset.
	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	agent := &store.AgentProfile{ID: "ws-agent", Slug: "ws", Status: "active", SystemPrompt: "p"}

	result, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}

	// The slot decision must exist at the workspace position with empty
	// content (skipped_no_content).
	var wsDecisionFound bool
	for _, d := range result.Plan.Decisions {
		if d.SlotName == ctxpkg.SlotWorkspace {
			wsDecisionFound = true
			if d.Content != "" {
				t.Errorf("expected empty workspace content when cache unwired, got %q", d.Content)
			}
			if d.ReasonTag != "skipped_no_content" {
				t.Errorf("expected ReasonTag=skipped_no_content, got %q", d.ReasonTag)
			}
		}
	}
	if !wsDecisionFound {
		t.Errorf("SlotWorkspace decision missing from plan even when content is empty")
	}
}

// TestAssembleSlots_WorkspaceSlotRefreshedAfterMtimeChange — the
// post-compaction acceptance test. Compaction tears down per-turn state
// and the NEXT AssembleSlots call rebuilds the slot store; the workspace
// cache must observe an updated mtime on AGENTS.md and refresh.
func TestAssembleSlots_WorkspaceSlotRefreshedAfterMtimeChange(t *testing.T) {
	repoRoot := t.TempDir()
	mkgitDir(t, repoRoot)
	agentsMD := filepath.Join(repoRoot, "AGENTS.md")
	mkfs(t, agentsMD, "RULES-V1")

	s, err := storetest.New(t, context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sess := &store.Session{ID: "ws-refresh-sess"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	client := chat.NewContextClient(s)
	client.WorkspaceCache = workspace.NewCache()
	client.WorkingDirForSession = func(*store.Session) (string, error) {
		return repoRoot, nil
	}
	svc := NewContextService(ContextServiceConfig{Client: client})

	agent := &store.AgentProfile{ID: "ws-agent", Slug: "ws", Status: "active", SystemPrompt: "p"}

	// First call — fresh walk-up, picks up V1.
	first, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("first AssembleSlots: %v", err)
	}
	firstWS := firstSlotBlock(first.Blocks, ctxpkg.SlotWorkspace)
	if !strings.Contains(firstWS.Content, "RULES-V1") {
		t.Fatalf("first call missing RULES-V1: %q", firstWS.Content)
	}

	// Rewrite AGENTS.md + bump mtime forward (simulating a real edit).
	if err := os.WriteFile(agentsMD, []byte("RULES-V2"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	future := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(agentsMD, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Second call (post-compaction equivalent — slot store rebuilt
	// from scratch via AssembleSlotSources). Must observe V2.
	second, err := svc.AssembleSlots(context.Background(), sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("second AssembleSlots: %v", err)
	}
	secondWS := firstSlotBlock(second.Blocks, ctxpkg.SlotWorkspace)
	if !strings.Contains(secondWS.Content, "RULES-V2") {
		t.Errorf("second call missed mtime refresh; expected RULES-V2, got %q", secondWS.Content)
	}
	if strings.Contains(secondWS.Content, "RULES-V1") {
		t.Errorf("stale RULES-V1 leaked through cache after mtime change: %q", secondWS.Content)
	}
}

func firstSlotBlock(blocks []ctxpkg.SlotBlock, name string) ctxpkg.SlotBlock {
	for _, b := range blocks {
		if b.SlotName == name {
			return b
		}
	}
	return ctxpkg.SlotBlock{}
}
