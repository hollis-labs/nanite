package tool

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

func setupTestCache(t *testing.T) (*ResultCache, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE tool_result_cache (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		byte_size INTEGER NOT NULL,
		was_truncated INTEGER NOT NULL DEFAULT 0,
		body TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewResultCache(db, ResultCacheConfig{
		SoftTruncBytes:  100,  // small for testing
		HardCapBytes:    1000, // small for testing
		CacheTTLSeconds: 3600,
	})
	return cache, db
}

func TestResultCache_SmallResult(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	body := "small result"
	visible, cached, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("small result should not be cached")
	}
	if visible != body {
		t.Errorf("expected original body, got: %s", visible)
	}
}

func TestResultCache_LargeResult_StoreAndFetch(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	body := strings.Repeat("x", 200) // over 100 byte soft threshold
	visible, cached, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("large result should be cached")
	}
	if !strings.Contains(visible, "[TRUNCATED") {
		t.Error("expected truncation pointer")
	}
	if !strings.Contains(visible, "tool_result://") {
		t.Error("expected tool_result:// pointer")
	}

	// Extract ID from pointer.
	idx := strings.Index(visible, "tool_result://")
	idStart := idx + len("tool_result://")
	idEnd := strings.Index(visible[idStart:], " ")
	id := visible[idStart : idStart+idEnd]

	// Fetch full body.
	slice, totalSize, err := cache.Fetch("sess-1", id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if totalSize != 200 {
		t.Errorf("expected total 200, got %d", totalSize)
	}
	if slice != body {
		t.Error("fetched body doesn't match original")
	}

	// Fetch with offset and length.
	slice2, _, err := cache.Fetch("sess-1", id, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if slice2 != body[10:30] {
		t.Errorf("expected slice from 10-30, got: %s", slice2)
	}
}

func TestResultCache_HardCap(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	body := strings.Repeat("x", 1500) // over 1000 byte hard cap
	_, cached, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}
	if !cached {
		t.Error("should be cached")
	}

	// The fetch should fail because body=NULL for over-hard-cap.
	var id string
	err = db.QueryRow(`SELECT id FROM tool_result_cache WHERE session_id = 'sess-1'`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = cache.Fetch("sess-1", id, 0, 0)
	if err == nil {
		t.Error("expected error for hard-cap entry")
	}
	if !strings.Contains(err.Error(), "hard cap") {
		t.Errorf("expected hard cap error, got: %v", err)
	}
}

func TestResultCache_Search(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	lines := []string{
		"line 1: hello world",
		"line 2: foo bar",
		"line 3: hello again",
		"line 4: nothing here",
		"line 5: hello final",
	}
	body := strings.Repeat("x", 50) + "\n" + strings.Join(lines, "\n") + "\n" + strings.Repeat("y", 50)
	_, _, err := cache.StoreResult("sess-1", "call-1", "test_tool", body)
	if err != nil {
		t.Fatal(err)
	}

	var id string
	err = db.QueryRow(`SELECT id FROM tool_result_cache WHERE session_id = 'sess-1'`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	matches, err := cache.Search("sess-1", id, "hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matches))
	}
}

func TestResultCache_Purge(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	// Insert an already-expired entry.
	_, err := db.Exec(
		`INSERT INTO tool_result_cache (id, session_id, tool_name, tool_call_id, created_at, expires_at, byte_size, was_truncated, body)
		 VALUES ('expired-1', 'sess-1', 'tool', 'call', '2020-01-01T00:00:00Z', '2020-01-01T01:00:00Z', 10, 0, 'data')`,
	)
	if err != nil {
		t.Fatal(err)
	}

	count, err := cache.Purge()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 purged, got %d", count)
	}
}

func TestResultCache_FetchNotFound(t *testing.T) {
	cache, db := setupTestCache(t)
	defer db.Close()

	_, _, err := cache.Fetch("sess-1", "nonexistent", 0, 0)
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

// TestTruncateAtBoundary_LineBoundary asserts that the helper prefers to cut
// at a newline rather than in the middle of a line (CW-20260426-0011).
func TestTruncateAtBoundary_LineBoundary(t *testing.T) {
	// Build a body where the soft threshold falls in the middle of a line.
	// Lines are 20 bytes each; threshold is set at 35 — lands mid-line-2.
	// Expected: cut at the end of line 1 (position 20, the \n index).
	line1 := strings.Repeat("a", 19) + "\n" // 20 bytes
	line2 := strings.Repeat("b", 19) + "\n" // 20 bytes
	body := line1 + line2 + strings.Repeat("c", 20)

	cut := truncateAtBoundary(body, 35)
	// cut should be at the \n in line1, i.e. index 19 (just before the \n).
	if cut != 19 {
		t.Errorf("expected cut at 19 (end of line1 before \\n), got %d", cut)
	}
	// Verify the truncated prefix ends with a complete line (no partial content).
	prefix := body[:cut]
	if strings.Contains(prefix, "b") {
		t.Error("truncated prefix must not contain line2 content")
	}
}

// TestTruncateAtBoundary_UTF8 asserts that the helper never splits a multi-byte
// UTF-8 sequence (CW-20260426-0011).
func TestTruncateAtBoundary_UTF8(t *testing.T) {
	// "é" is 2 bytes (0xC3 0xA9). Build a string of 'a'*9 + "é" so the
	// 2-byte sequence straddles the threshold of 10.
	s := strings.Repeat("a", 9) + "é" + strings.Repeat("a", 10)
	cut := truncateAtBoundary(s, 10)
	// The cut must not land inside the 2-byte sequence.
	// Valid positions: 9 (before "é") or 11 (after "é" — but 11 > 10 so not taken).
	// truncateAtBoundary walks back, so it should land at 9.
	if cut != 9 {
		t.Errorf("expected cut at 9 (before multi-byte rune), got %d", cut)
	}
	// Ensure the slice is valid UTF-8.
	if !utf8.ValidString(s[:cut]) {
		t.Errorf("prefix is not valid UTF-8: %q", s[:cut])
	}
}

// TestTruncateAtBoundary_NoNewline asserts fallback to UTF-8-safe byte position
// when no newline precedes the threshold.
func TestTruncateAtBoundary_NoNewline(t *testing.T) {
	s := strings.Repeat("x", 200)
	cut := truncateAtBoundary(s, 50)
	if cut != 50 {
		t.Errorf("expected cut at 50 (no newline, plain ASCII), got %d", cut)
	}
}

// TestTruncateAtBoundary_BeyondLength asserts the full length is returned when
// maxBytes >= len(s).
func TestTruncateAtBoundary_BeyondLength(t *testing.T) {
	s := "hello"
	cut := truncateAtBoundary(s, 100)
	if cut != len(s) {
		t.Errorf("expected cut at %d, got %d", len(s), cut)
	}
}

// TestResultCache_TruncatesAtLineBoundary is the integration-level regression:
// a large result whose soft threshold falls mid-line must produce a visible
// preview that ends at a complete line, not mid-content (CW-20260426-0011).
func TestResultCache_TruncatesAtLineBoundary(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE tool_result_cache (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		byte_size INTEGER NOT NULL,
		was_truncated INTEGER NOT NULL DEFAULT 0,
		body TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}

	// soft threshold = 100 bytes; each item line is 30 bytes so threshold
	// falls mid-item-4. The preview must end at the \n after item-3.
	const softThreshold = 100
	cache := NewResultCache(db, ResultCacheConfig{
		SoftTruncBytes:  softThreshold,
		HardCapBytes:    1024 * 1024,
		CacheTTLSeconds: 3600,
	})

	// Build body: 10 lines of 30 bytes each (29 chars + \n).
	var bodyLines []string
	for i := 0; i < 10; i++ {
		bodyLines = append(bodyLines, strings.Repeat(string(rune('A'+i)), 29))
	}
	body := strings.Join(bodyLines, "\n") + "\n"

	visible, cached, err := cache.StoreResult("sess", "call", "tool", body)
	if err != nil {
		t.Fatalf("StoreResult: %v", err)
	}
	if !cached {
		t.Fatal("expected body to be cached (over soft threshold)")
	}

	// Extract just the preview portion (everything before [TRUNCATED).
	truncIdx := strings.Index(visible, "\n\n[TRUNCATED")
	if truncIdx < 0 {
		t.Fatalf("no truncation footer found in visible output: %q", visible)
	}
	preview := visible[:truncIdx]

	// The preview must end at a complete line: last byte before the footer
	// should be 'A'–'Z' (item content), and the preview must not contain a
	// partial item (we check that the last char of the last line is the
	// expected letter repeated 29 times).
	lastNL := strings.LastIndexByte(preview, '\n')
	var lastLine string
	if lastNL < 0 {
		lastLine = preview
	} else {
		lastLine = preview[lastNL+1:]
	}
	if len(lastLine) != 0 {
		// Every non-empty last line must be a full 29-byte item.
		if len(lastLine) != 29 {
			t.Errorf("last line of preview has unexpected length %d (want 0 or 29): %q",
				len(lastLine), lastLine)
		}
	}
	// Crucially: no partial item content — every character in the preview
	// must be consistent with our item alphabet.
	if !utf8.ValidString(preview) {
		t.Error("preview is not valid UTF-8")
	}
}

// torqueTaskRecordFixture mirrors the field order of Torque's real
// TaskRecord wire shape (a Go struct — encoding/json preserves declared
// field order, unlike a map). Description sits well before DependsOn, so a
// realistic task description reliably pushes DependsOn past the
// soft-truncation cutoff — this is the exact shape that produced the
// CW-20260815-0020 incident (a live Orchestrator got truncated
// torque_task_get results with depends_on cut off, and had no
// fetch_tool_result/search_tool_result grant to recover it).
type torqueTaskRecordFixture struct {
	ID          string            `json:"ID"`
	Title       string            `json:"Title"`
	Description string            `json:"Description"`
	Status      string            `json:"Status"`
	Priority    int               `json:"Priority"`
	Manual      bool              `json:"Manual"`
	Executor    string            `json:"Executor"`
	Tools       nullStringFixture `json:"Tools"`
	Permissions nullStringFixture `json:"Permissions"`
	Environment nullStringFixture `json:"Environment"`
	MaxRetries  int               `json:"MaxRetries"`
	OnDone      string            `json:"OnDone"`
	OnFail      string            `json:"OnFail"`
	OnReview    string            `json:"OnReview"`
	OnDoneMerge string            `json:"OnDoneMerge"`
	DependsOn   nullStringFixture `json:"DependsOn"`
	ProjectID   nullStringFixture `json:"ProjectID"`
	CreatedAt   string            `json:"CreatedAt"`
	UpdatedAt   string            `json:"UpdatedAt"`
	Kind        string            `json:"Kind"`
	Trust       string            `json:"Trust"`
}

type nullStringFixture struct {
	String string `json:"String"`
	Valid  bool   `json:"Valid"`
}

// CW-20260814-0015's real description text (one of the five original
// CW-20260815-0020 incident tasks), fetched live via torque_task_get during
// that investigation. Kept verbatim-in-spirit so the fixture's byte size is
// representative of this workspace's actual task-authoring style (full
// What/How-to-fix/Non-goals/Boot-prompt structure), not an arbitrary
// strings.Repeat filler.
const torqueTaskDescriptionFixture = `## What
The core of A2A protocol adoption (CW-20260813-0002). Implements the design doc's "Task lifecycle and routing" and "Persistence" sections — this is the piece that makes the "A2A is a protocol adapter, not a new execution substrate" principle real: every Task is fulfilled by exactly one of Nanite's two existing execution paths, never a third parallel mechanism.

## How to fix
- New a2a_tasks table (+ migration): task ID, target kind (workflow | instance), target reference, caller-supplied message, durable_agent_instance_id (set once routing resolves), derived TaskState (cached, refreshed on read and on state-change triggers), push notification config (nullable, populated by the later push-notifications ticket), created/updated timestamps. This table is bookkeeping and translation only — it points at workflow_runs/durable_agent_instances, it does not duplicate their state.
- TaskManager service implementation in internal/service (plain functions/methods, thin-wrapper-over-service-layer pattern, per internal/a2a's pure wire types from CW-20260814-0014). Two routing outcomes on submission:
  - Target = workflow skill: call the existing WorkflowLauncher.Launch (Agent Workflows pillar) to start a new template-class durable-agent instance. Store the resulting durable_agent_instance_id.
  - Target = existing instance address: call DurableWake.Wake() with Reason: DurableAgentWakeExternalMessage (making this enum value real for the first time) and WakePayload.Prompt set from the Task's message. This requires CW-20260814-0013 (Prompt injection fix) to have landed.
- TaskState must be derived from the real execution status being tracked, never independently maintained.
- Non-workflow-backed tasks get the coarser completion semantics the design doc specifies.

## Non-goals
- No input-required state — that's CW-20260814-0016, which depends on this one.
- No JSON-RPC/HTTP transport — that's CW-20260814-0017, which depends on this one.
- No push notification delivery — separate ticket, depends on this one and the transport ticket.

## Boot prompt
You're picking up the core routing ticket for A2A protocol adoption, in the Nanite repo. Before starting, confirm CW-20260814-0013 and CW-20260814-0014 are actually landed. Fetch this task's full description via torque_task_get for complete context, then read the design docs in full. Downstream tickets all depend on this one landing.`

// TestResultCache_TruncatedTorqueTaskRecovery is the permanent regression
// test for CW-20260815-0020. It replaces a one-time reproduction test that
// was written, run, and deleted during that investigation — this is the
// same approach (a real-shaped fixture, production thresholds) kept in the
// suite so a future change to Torque's TaskRecord field order or to
// DefaultSoftTruncBytes can't silently reintroduce the bug with nothing to
// catch it.
//
// Uses production defaults throughout (DefaultSoftTruncBytes,
// DefaultHardCapBytes, DefaultCacheTTLSeconds) — not a lowered test
// threshold — so the test fails if those defaults ever change in a way
// that stops a realistic task record from truncating.
func TestResultCache_TruncatedTorqueTaskRecovery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE tool_result_cache (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		byte_size INTEGER NOT NULL,
		was_truncated INTEGER NOT NULL DEFAULT 0,
		body TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}

	cache := NewResultCache(db, ResultCacheConfig{
		SoftTruncBytes:  DefaultSoftTruncBytes,
		HardCapBytes:    DefaultHardCapBytes,
		CacheTTLSeconds: DefaultCacheTTLSeconds,
	})

	record := torqueTaskRecordFixture{
		ID:          "CW-20260814-0015",
		Title:       "A2A: Task persistence + TaskManager — route Task submissions to Workflow launch or durable-agent wake",
		Description: torqueTaskDescriptionFixture,
		Status:      "todo",
		Priority:    3,
		Manual:      true,
		Executor:    "cli",
		MaxRetries:  3,
		OnDone:      "review",
		OnFail:      "retry",
		OnReview:    "pause",
		OnDoneMerge: "none",
		DependsOn:   nullStringFixture{String: `["CW-20260814-0013","CW-20260814-0014"]`, Valid: true},
		ProjectID:   nullStringFixture{String: "PRJ-20260417-0002", Valid: true},
		CreatedAt:   "2026-08-14T23:31:34Z",
		UpdatedAt:   "2026-08-14T23:31:34Z",
		Kind:        "agent",
		Trust:       "normal",
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= DefaultSoftTruncBytes {
		t.Fatalf("fixture body is %d bytes, must exceed DefaultSoftTruncBytes (%d) for this test to be meaningful — the fixture no longer reproduces a realistic truncating call", len(body), DefaultSoftTruncBytes)
	}

	visible, cached, err := cache.StoreResult("sess-torque", "call-1", "torque_task_get", string(body))
	if err != nil {
		t.Fatalf("StoreResult: %v", err)
	}
	if !cached {
		t.Fatal("expected the record to be cached (exceeds soft threshold)")
	}
	if !strings.Contains(visible, "tool_result://") {
		t.Fatal("expected tool_result:// pointer footer in visible output")
	}
	if strings.Contains(visible, `"DependsOn"`) {
		t.Fatal("DependsOn must NOT be visible in the truncated preview — if this fails, the fixture (or the field order it models) no longer reproduces the incident condition")
	}

	// Extract the ULID the same way an agent reads it off the footer.
	idx := strings.Index(visible, "tool_result://")
	rest := visible[idx+len("tool_result://"):]
	end := strings.IndexAny(rest, " \n")
	id := rest[:end]

	// fetch_tool_result's underlying call.
	full, totalSize, err := cache.Fetch("sess-torque", id, 0, 0)
	if err != nil {
		t.Fatalf("Fetch (fetch_tool_result): %v", err)
	}
	if totalSize != len(body) {
		t.Fatalf("Fetch totalSize=%d, want %d", totalSize, len(body))
	}
	if !strings.Contains(full, `"DependsOn"`) {
		t.Fatal("fetch_tool_result did not recover DependsOn — regression of the CW-20260815-0020 fix")
	}
	if !strings.Contains(full, "CW-20260814-0013") || !strings.Contains(full, "CW-20260814-0014") {
		t.Fatal("fetch_tool_result recovered the DependsOn key but not its dependency IDs")
	}

	// search_tool_result's underlying call.
	matches, err := cache.Search("sess-torque", id, "DependsOn", 5)
	if err != nil {
		t.Fatalf("Search (search_tool_result): %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("search_tool_result found no matches for DependsOn — regression of the CW-20260815-0020 fix")
	}
	found := false
	for _, m := range matches {
		if strings.Contains(m.Context, "CW-20260814-0013") {
			found = true
		}
	}
	if !found {
		t.Fatal("search_tool_result match context did not include the dependency ID")
	}
}
