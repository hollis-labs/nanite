package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestCognitiveCreateRefusesProfileIdentityWithoutEffects(t *testing.T) {
	s := newTestStore(t)
	h := makeTestHost(t, s, "no-profile-binding")
	view := &Session{ID: "refused-view", Provider: "anthropic"}
	if err := s.CreateCognitiveSession(t.Context(), view, h.ID); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatal(err)
	}
	if _, err := s.GetSession(t.Context(), view.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("refused view persisted", err)
	}
}
func TestCognitiveCreateCommitsDefinitionAndRejectsFailedConfigWrite(t *testing.T) {
	s := newTestStore(t)
	h := makeTestHost(t, s, "native-view")
	view := &Session{ID: "native-view", Provider: "anthropic", Metadata: `{"label":"example"}`}
	record := CognitiveViewRecord{DefinitionRefJSON: `{"id":"verified-test-pin"}`, ChatConfigJSON: `{"instructions":"private fixture"}`}
	if _, err := s.DB.ExecContext(t.Context(), `CREATE TRIGGER refuse_config BEFORE INSERT ON cognitive_views BEGIN SELECT RAISE(ABORT,'fixture failure');END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDefinedSessionWithHost(t.Context(), view, record, HostSettingsAdmission{ID: h.ID, Revision: h.Revision}); err == nil {
		t.Fatal("failed config accepted")
	}
	if _, err := s.GetSession(t.Context(), view.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("partial view persisted", err)
	}
	if _, err := s.DB.ExecContext(t.Context(), `DROP TRIGGER refuse_config`); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDefinedSessionWithHost(t.Context(), view, record, HostSettingsAdmission{ID: h.ID, Revision: h.Revision}); err != nil {
		t.Fatal(err)
	}
	runtime, err := s.GetSessionSubagentRuntime(t.Context(), view.ID)
	if err != nil || runtime != "api" {
		t.Fatal(runtime, err)
	}
	persisted, err := s.GetCognitiveView(t.Context(), view.ID)
	if err != nil || persisted.ChatConfigJSON != record.ChatConfigJSON {
		t.Fatal(persisted, err)
	}
	if _, err := s.GetSessionPrimaryAgent(t.Context(), view.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("definition became actor", err)
	}
}
