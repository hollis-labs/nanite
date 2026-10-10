package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCodeModeParentHistoryScopedSnapshotPagination(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	other := &Session{}
	if operationErr := s.CreateSession(t.Context(), other); operationErr != nil {
		t.Fatal(operationErr)
	}
	if operationErr := s.CreateMessage(t.Context(), &Message{SessionID: other.ID, Role: "user", Content: "needle FOREIGN SECRET"}); operationErr != nil {
		t.Fatal(operationErr)
	}
	verifier := codeModePrivateHost()
	fork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "search context"}})
	if err != nil {
		t.Fatal(err)
	}
	// Public presentation metadata cannot select another parent or snapshot.
	if operationErr := s.UpdateSessionMetadata(t.Context(), fork.ID, `{"parent_view_id":"`+other.ID+`","fork_kind":"code_mode"}`); operationErr != nil {
		t.Fatal(operationErr)
	}
	before := codeModeState(t, s)
	var cursor, digest string
	var ids []string
	for {
		page, pageErr := s.SearchCodeModeParentHistory(t.Context(), fork.ID, CodeModeHistoryRequest{Query: "needle", Cursor: cursor, Limit: 2}, verifier)
		if pageErr != nil || len(page.Matches) == 0 || len(page.Matches) > 2 {
			t.Fatalf("history page=%+v,%v", page, pageErr)
		}
		if digest == "" {
			digest = page.SnapshotDigest
		} else if digest != page.SnapshotDigest {
			t.Fatal("pagination switched snapshots")
		}
		for _, match := range page.Matches {
			if match.ParentViewID != parent.ID || strings.Contains(match.Content, "SECRET") || strings.Contains(match.Content, "PRIVATE") || strings.Contains(match.Content, "FOREIGN") {
				t.Fatalf("history escaped verified parent: %+v", match)
			}
			ids = append(ids, match.MessageID)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(ids) != 6 || verifier.historyCalls != 3 || !reflect.DeepEqual(before, codeModeState(t, s)) {
		t.Fatalf("history effects/repetition: ids=%v hostreads=%d", ids, verifier.historyCalls)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("history repeated %s", id)
		}
		seen[id] = true
	}
	first, err := s.SearchCodeModeParentHistory(t.Context(), fork.ID, CodeModeHistoryRequest{Query: "needle", Limit: 1}, verifier)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("missing page cursor: %+v,%v", first, err)
	}
	for _, request := range []CodeModeHistoryRequest{
		{Query: "changed", Cursor: first.NextCursor, Limit: 1},
		{Query: "needle", Cursor: "malformed", Limit: 1},
		{Query: "needle", Limit: 0},
		{Query: "needle", Limit: 33},
		{Query: strings.Repeat("x", 257), Limit: 1},
	} {
		if page, pageErr := s.SearchCodeModeParentHistory(t.Context(), fork.ID, request, verifier); pageErr == nil || len(page.Matches) != 0 || !reflect.DeepEqual(before, codeModeState(t, s)) {
			t.Fatalf("invalid page admitted: %+v,%v", page, pageErr)
		}
	}
	secondFork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "another context"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SearchCodeModeParentHistory(t.Context(), secondFork.ID, CodeModeHistoryRequest{Query: "needle", Cursor: first.NextCursor, Limit: 1}, verifier); err == nil {
		t.Fatal("cursor was retargeted to another fork")
	}
	// Search is literal, not SQL/regex/global inventory expansion.
	for _, query := range []string{"%", "' OR 1=1 --", "FOREIGN"} {
		page, pageErr := s.SearchCodeModeParentHistory(t.Context(), fork.ID, CodeModeHistoryRequest{Query: query, Limit: 2}, verifier)
		if pageErr != nil || len(page.Matches) != 0 || page.NextCursor != "" {
			t.Fatalf("literal query %q expanded visibility: %+v,%v", query, page, pageErr)
		}
	}
}

func TestCodeModeParentHistoryRefusalAndDrift(t *testing.T) {
	var absent *Store
	if _, err := absent.SearchCodeModeParentHistory(t.Context(), "claimed-fork", CodeModeHistoryRequest{}, nil); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal("missing verifier accessed inventory", err)
	}
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	verifier := codeModePrivateHost()
	fork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "search"}})
	if err != nil {
		t.Fatal(err)
	}
	before := codeModeState(t, s)
	for _, denied := range []CodeModeForkVerifier{nil, &codeModeTestVerifier{callerErr: ErrVerifiedActorRequired}, &codeModeTestVerifier{limits: verifier.limits, historyErr: ErrVerifiedActorRequired}} {
		page, pageErr := s.SearchCodeModeParentHistory(t.Context(), fork.ID, CodeModeHistoryRequest{Query: "needle", Limit: 2}, denied)
		if !errors.Is(pageErr, ErrVerifiedActorRequired) || len(page.Matches) != 0 || !reflect.DeepEqual(before, codeModeState(t, s)) {
			t.Fatalf("foreign/revoked reader effects: %+v,%v", page, pageErr)
		}
	}
	verifier.onHistory = func() {
		if operationErr := s.CreateMessage(t.Context(), &Message{SessionID: parent.ID, Role: "user", Content: "needle NEW AUTHORITY"}); operationErr != nil {
			t.Fatal(operationErr)
		}
	}
	page, pageErr := s.SearchCodeModeParentHistory(t.Context(), fork.ID, CodeModeHistoryRequest{Query: "needle", Limit: 2}, verifier)
	if !errors.Is(pageErr, ErrCodeModeSnapshot) || len(page.Matches) != 0 {
		t.Fatalf("callback-time parent change leaked old/new page: %+v,%v", page, pageErr)
	}
	verifier.onHistory = nil
	page, pageErr = s.SearchCodeModeParentHistory(t.Context(), fork.ID, CodeModeHistoryRequest{Query: "needle", Limit: 2}, verifier)
	if !errors.Is(pageErr, ErrCodeModeSnapshot) || len(page.Matches) != 0 {
		t.Fatalf("snapshot silently became live history: %+v,%v", page, pageErr)
	}
}

func TestCodeModeForkPreservesClearBoundaryAndAtomicFailure(t *testing.T) {
	s := newTestStore(t)
	parent := codeModeParent(t, s)
	if _, err := s.ClearConversation(t.Context(), parent.ID, false); err != nil {
		t.Fatal(err)
	}
	if operationErr := s.CreateMessage(t.Context(), &Message{SessionID: parent.ID, Role: "user", Content: "working after clear"}); operationErr != nil {
		t.Fatal(operationErr)
	}
	verifier := codeModePrivateHost()
	for _, full := range []bool{false, true} {
		fork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "task", LastN: 8, FullHistory: full}})
		if err != nil {
			t.Fatal(err)
		}
		working, err := s.ListWorkingMessages(t.Context(), fork.ID, 64)
		if err != nil || len(working) != 2 || working[0].Content != "working after clear" {
			t.Fatalf("full=%v revived cleared working context: %+v,%v", full, working, err)
		}
		var boot map[string]json.RawMessage
		if operationErr := json.Unmarshal([]byte(working[1].Content), &boot); operationErr != nil || string(boot["goal"]) != `"task"` {
			t.Fatalf("bootstrap lost goal: %+v,%v", boot, operationErr)
		}
	}
	before := codeModeState(t, s)
	if _, err := s.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_code_context BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'owned context fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	fork, err := s.ForkCodeModeSession(t.Context(), parent.ID, CodeModeForkOptions{Verifier: verifier, Request: CodeModeForkRequest{Goal: "must rollback"}})
	if err == nil || fork != nil || !reflect.DeepEqual(before, codeModeState(t, s)) {
		t.Fatalf("failed context insert retained partial child: %+v,%v", fork, err)
	}
}
