package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/libs/util/sqlite/sqlitekit"
)

func TestClearConversation_RetainsHistoryAndPinsAcrossReopen(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(map[bool]string{false: "drop", true: "keep"}[keep], func(t *testing.T) {
			s := newTestStore(t)
			ctx := t.Context()
			if err := s.CreateSession(ctx, &Session{ID: "clear", IsPinned: true}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetSessionContextPrompt(ctx, "clear", "pinned instruction"); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateDocument(ctx, &Document{ID: "pin", SessionID: "clear", Name: "design", Content: "pinned document", Included: true, FullContent: true}); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateMessage(ctx, &Message{ID: "old", SessionID: "clear", Role: "user", Content: "old working content"}); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertHandoffStash(ctx, HandoffStash{ID: "stash", SessionID: "clear", Payload: `{"v":1}`, CreatedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateAgentRuntimeRow(ctx, &AgentRuntimeRow{ID: "clear", Mode: "long_lived", State: "running", ProviderSessionID: "old-provider-thread"}); err != nil {
				t.Fatal(err)
			}
			cut, err := s.ClearConversation(ctx, "clear", keep)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.CreateMessage(ctx, &Message{ID: "new", SessionID: "clear", Role: "user", Content: "new working content"}); err != nil {
				t.Fatal(err)
			}
			// Equal timestamps cannot let pre-clear rows enter the working window.
			if _, err = s.DB.ExecContext(ctx, `UPDATE messages SET created_at = '2026-10-09T00:00:00Z' WHERE session_id = 'clear'`); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			db, err := sqlitekit.OpenSingle(ctx, s.dbPath, sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions()})
			if err != nil {
				t.Fatal(err)
			}
			s.DB = db
			if _, err = s.DB.ExecContext(ctx, `VACUUM`); err != nil {
				t.Fatal(err)
			}
			working, err := s.ListWorkingMessages(ctx, "clear", 100)
			if err != nil || len(working) != 1 || working[0].ID != "new" {
				t.Fatalf("working=%+v err=%v", working, err)
			}
			history, err := s.ListMessages(ctx, "clear", 100)
			if err != nil || len(history) != 3 || history[1].ID != cut.MessageID {
				t.Fatalf("history=%+v err=%v", history, err)
			}
			paged, err := s.ListMessagesPaginated(ctx, "clear", 100, 0)
			if err != nil || paged.Messages[1].ID != cut.MessageID {
				t.Fatalf("page=%+v err=%v", paged, err)
			}
			prompt, err := s.GetSessionContextPrompt(ctx, "clear")
			if err != nil || prompt != "pinned instruction" {
				t.Fatalf("pin=%q %v", prompt, err)
			}
			docs, err := s.GetIncludedDocuments(ctx, "clear")
			if err != nil || len(docs) != 1 || docs[0].Content != "pinned document" {
				t.Fatalf("documents=%+v %v", docs, err)
			}
			session, err := s.GetSession(ctx, "clear")
			if err != nil || !session.IsPinned {
				t.Fatalf("session pin=%+v %v", session, err)
			}
			providerID, err := s.AgentRuntimeProviderSessionID(ctx, "clear")
			if err != nil || providerID != "" {
				t.Fatalf("provider resume=%q %v", providerID, err)
			}
			_, err = s.GetLatestStashForSession(ctx, "clear")
			if keep && err != nil || !keep && !errors.Is(err, ErrHandoffStashNotFound) {
				t.Fatalf("keep=%v stash=%v", keep, err)
			}
		})
	}
}

func TestClearConversation_RollsBackAllResetEffects(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if err := s.CreateSession(ctx, &Session{ID: "rollback"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertHandoffStash(ctx, HandoffStash{ID: "stash", SessionID: "rollback", CreatedAt: "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAgentRuntimeRow(ctx, &AgentRuntimeRow{ID: "rollback", Mode: "long_lived", State: "running", ProviderSessionID: "retained"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER refuse_clear BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'fixture outage'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearConversation(ctx, "rollback", false); err == nil {
		t.Fatal("clear should fail")
	}
	cut, err := s.LatestConversationClear(ctx, "rollback")
	if err != nil || cut != nil {
		t.Fatalf("boundary=%+v %v", cut, err)
	}
	history, err := s.ListMessages(ctx, "rollback", 100)
	if err != nil || len(history) != 0 {
		t.Fatalf("partial marker=%+v %v", history, err)
	}
	if _, err = s.GetLatestStashForSession(ctx, "rollback"); err != nil {
		t.Fatal(err)
	}
	pid, err := s.AgentRuntimeProviderSessionID(ctx, "rollback")
	if err != nil || pid != "retained" {
		t.Fatalf("resume=%q %v", pid, err)
	}
	if _, err = s.ClearConversation(ctx, "missing", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing=%v", err)
	}
}

func TestClearConversation_RequestMetadataCannotEstablishBoundary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateSession(ctx, &Session{ID: "untrusted"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMessage(ctx, &Message{SessionID: "untrusted", Role: "user", Content: "retained", Metadata: `{"conversation_cleared":{"message_id":"invented"}}`}); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.ListWorkingMessages(ctx, "untrusted", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("metadata hid history: %+v %v", msgs, err)
	}
}

func TestClearConversation_CopyAndForkCarryAuthoritativeBoundary(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if err := s.CreateSession(ctx, &Session{ID: "source"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMessage(ctx, &Message{SessionID: "source", Role: "user", Content: "cleared history"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClearConversation(ctx, "source", false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMessage(ctx, &Message{SessionID: "source", Role: "user", Content: "working history"}); err != nil {
		t.Fatal(err)
	}
	fork, err := s.ForkSession(ctx, "source", &Session{ID: "fork"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession(ctx, &Session{ID: "copy"}); err != nil {
		t.Fatal(err)
	}
	if err = s.CopyMessages(ctx, "source", "copy"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{fork.ID, "copy"} {
		working, workingErr := s.ListWorkingMessages(ctx, id, 100)
		if workingErr != nil || len(working) != 1 || working[0].Content != "working history" {
			t.Fatalf("%s working=%+v %v", id, working, workingErr)
		}
		full, fullErr := s.ListMessages(ctx, id, 100)
		if fullErr != nil || len(full) != 3 {
			t.Fatalf("%s full=%+v %v", id, full, fullErr)
		}
	}
}

func TestClearConversation_PreservesAgentAssignmentHistoryAndGrants(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	agent := makeTestAgent(t, s, "clear-authority")
	toolID, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO actor_granted_tools(agent_id,tool_id,granted_via,created_at) VALUES(?,?,'explicit','private-fixture-time')`, agent.ID, toolID); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateSession(ctx, &Session{ID: "authority"}); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureSessionAgent(ctx, "authority", agent.ID, "", true); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetAgentForActor(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClearConversation(ctx, "authority", false); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetAgentForActor(ctx, agent.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("profile changed: before=%+v after=%+v err=%v", before, after, err)
	}
	binding, err := s.GetSessionPrimaryAgent(ctx, "authority")
	if err != nil || binding.AgentID != agent.ID {
		t.Fatalf("binding=%+v %v", binding, err)
	}
	names, err := s.ListAgentToolNames(ctx, agent.ID)
	if err != nil || len(names) != 1 || names[0] != "dev_read" {
		t.Fatalf("grants=%v %v", names, err)
	}
	explicit, err := s.HasAgentToolGrantedVia(ctx, agent.ID, "explicit")
	if err != nil || !explicit {
		t.Fatalf("provenance changed: %v %v", explicit, err)
	}
}
