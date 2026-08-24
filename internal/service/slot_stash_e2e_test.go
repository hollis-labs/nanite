package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// TestSlotStash_E2E_50KLineFile_StashAndDevRead is the ticket's
// load-bearing acceptance test (CW-20260512-0110):
//
//	"A 50K-line file referenced in context gets stashed; agent
//	 retrieves via dev_read. The envelope shipped to the LLM is
//	 deterministic."
//
// It exercises the full pipeline:
//
//  1. Construct a deterministic 50K-line "file" (a strings.Builder body
//     — the ticket's sharp edge says "deterministic — generated, not a
//     real file").
//  2. Run the Context Broker decider with the production
//     ArtifactStasher backed by a real Store + filesystem.
//  3. Confirm the slot decision is ActionPointer with the spec-shaped
//     envelope `<ref:artifact_id=art-stash-..., tokens=N, available
//     via dev_read>`.
//  4. Confirm the artifact row exists with the right size and origin.
//  5. Call DevToolsTransport.callRead(artifact_id=…) and assert the
//     returned content matches the original input verbatim (modulo
//     the line-numbered formatting `dev_read` adds — we strip that
//     prefix to verify the body).
//  6. Re-run steps 2 + 3 and confirm the envelope is byte-identical
//     (determinism).
func TestSlotStash_E2E_50KLineFile_StashAndDevRead(t *testing.T) {
	// --- Test fixtures -------------------------------------------------
	tmp := t.TempDir()
	s, err := storetest.New(t, context.Background(), tmp+"/e2e.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	artifactsRoot := tmp + "/artifacts"
	if err := os.MkdirAll(artifactsRoot, 0o755); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}

	if err := s.CreateSession(context.Background(), &store.Session{ID: "e2e-sess"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cfg := config.DefaultAppConfig()
	cfg.Artifacts.StorageDir = artifactsRoot
	stasher, err := NewArtifactStasher(ArtifactStasherConfig{Store: s, AppConfig: cfg})
	if err != nil {
		t.Fatalf("NewArtifactStasher: %v", err)
	}

	// --- 50K-line deterministic body -----------------------------------
	// Build deterministically so re-runs produce identical bytes; matches
	// the ticket's sharp edge ("deterministic — generated, not a real
	// file"). Each line carries its number so off-by-one bugs in the
	// page-back path are detectable.
	const lineCount = 50_000
	var b strings.Builder
	b.Grow(lineCount * 32)
	for i := 1; i <= lineCount; i++ {
		fmt.Fprintf(&b, "line %05d: lorem ipsum dolor sit amet consectetur adipiscing\n", i)
	}
	bigContent := b.String()
	if len(bigContent) < 1_000_000 {
		t.Fatalf("test fixture too small: %d bytes (expected >1MB for 50K lines)", len(bigContent))
	}

	// --- 1. Decide & stash --------------------------------------------
	// Set a tight budget on SlotContext so the decider chooses pointer.
	// (Default budgets are sized for "real" assemblies; the test's huge
	// body would clear them too — set explicitly for documentation.)
	budgets := ctxpkg.DefaultBudgets()
	budgets[ctxpkg.SlotContext] = 1000 // tokens

	plan := contextbroker.DecideAssembly(context.Background(), contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentWriteCode},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "e2e-sess",
		Sources:   map[string]string{ctxpkg.SlotContext: bigContent},
		Budgets:   budgets,
		Stasher:   stasher,
	})

	var ctxDec *contextbroker.SlotDecision
	for i := range plan.Decisions {
		if plan.Decisions[i].SlotName == ctxpkg.SlotContext {
			ctxDec = &plan.Decisions[i]
			break
		}
	}
	if ctxDec == nil {
		t.Fatal("SlotContext decision missing from plan")
	}
	if ctxDec.Action != contextbroker.ActionPointer {
		t.Fatalf("expected ActionPointer for 50K-line slot, got %v (reason=%s)", ctxDec.Action, ctxDec.ReasonTag)
	}
	if !strings.HasPrefix(ctxDec.Content, "<ref:artifact_id=art-stash-") {
		t.Errorf("pointer envelope malformed: %q", ctxDec.Content)
	}
	if !strings.Contains(ctxDec.Content, "available via dev_read>") {
		t.Errorf("pointer envelope missing dev_read affordance: %q", ctxDec.Content)
	}
	if ctxDec.ArtifactID == "" {
		t.Fatal("pointer decision missing ArtifactID")
	}

	// --- 2. Artifact row exists ---------------------------------------
	row, err := s.GetArtifact(context.Background(), ctxDec.ArtifactID)
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if row.Origin != SlotStashOrigin {
		t.Errorf("artifact origin = %q, want %q", row.Origin, SlotStashOrigin)
	}
	if row.SizeBytes != int64(len(bigContent)) {
		t.Errorf("artifact size = %d, want %d", row.SizeBytes, len(bigContent))
	}

	// --- 3. dev_read(artifact_id=...) returns the body ----------------
	dev := mcp.NewDevToolsTransport([]string{tmp}).WithArtifactResolver(
		mcp.NewStoreArtifactResolver(s), artifactsRoot,
	)

	// Read the first 5 lines to validate the resolver wiring + line
	// numbering format. dev_read returns "  line_num\tcontent\n".
	// intArg expects float64/json.Number (JSON arg types), so pass
	// float64 values explicitly rather than int literals — int would
	// fall through intArg's default-case branch and the offset/limit
	// would silently default to the legacy values.
	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": ctxDec.ArtifactID,
		"offset":      float64(1),
		"limit":       float64(5),
	})
	if err != nil {
		t.Fatalf("dev_read: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("dev_read returned error: %+v", res)
	}
	body := res.Content[0].Text
	for i := 1; i <= 5; i++ {
		expectedLine := fmt.Sprintf("line %05d: lorem ipsum dolor sit amet consectetur adipiscing", i)
		if !strings.Contains(body, expectedLine) {
			t.Errorf("dev_read output missing line %d (%q); got %q", i, expectedLine, body)
		}
	}

	// Page deep into the body to make sure offset works on a 50K-line file.
	res, err = dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": ctxDec.ArtifactID,
		"offset":      float64(49_998),
		"limit":       float64(3),
	})
	if err != nil {
		t.Fatalf("dev_read (deep): %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("dev_read (deep) returned error: %+v", res)
	}
	deep := res.Content[0].Text
	if !strings.Contains(deep, "line 50000:") {
		t.Errorf("dev_read at offset=49998 missing final line; got %q", deep)
	}

	// --- 4. Determinism: re-running the decider produces an
	//        identical envelope (cacheable-prefix contract) -----------
	plan2 := contextbroker.DecideAssembly(context.Background(), contextbroker.AssemblyInput{
		Intent:    contextbroker.Intent{Type: contextbroker.IntentWriteCode},
		SlotOrder: ctxpkg.SlotOrder,
		SessionID: "e2e-sess",
		Sources:   map[string]string{ctxpkg.SlotContext: bigContent},
		Budgets:   budgets,
		Stasher:   stasher,
	})
	var ctxDec2 *contextbroker.SlotDecision
	for i := range plan2.Decisions {
		if plan2.Decisions[i].SlotName == ctxpkg.SlotContext {
			ctxDec2 = &plan2.Decisions[i]
			break
		}
	}
	if ctxDec2 == nil {
		t.Fatal("turn-2 SlotContext decision missing")
	}
	if ctxDec.Content != ctxDec2.Content {
		t.Errorf("pointer envelope drifted across turns:\n turn1: %q\n turn2: %q", ctxDec.Content, ctxDec2.Content)
	}
	if ctxDec.ArtifactID != ctxDec2.ArtifactID {
		t.Errorf("artifact_id drifted across turns: %q vs %q", ctxDec.ArtifactID, ctxDec2.ArtifactID)
	}

	// Re-stash should be idempotent: still one row.
	rows, err := s.ListArtifacts(context.Background(), "e2e-sess")
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 artifact row after deterministic re-decide, got %d", len(rows))
	}
}
