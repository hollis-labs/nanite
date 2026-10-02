package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type querySessionFixture struct {
	SessionService
	calls int
}

func (s *querySessionFixture) Get(_ context.Context, id string) (*store.Session, error) {
	s.calls++
	if id != "allowed" {
		return nil, sql.ErrNoRows
	}
	return &store.Session{ID: id}, nil
}

func TestPluginQueryContentRequiresSeparateScope(t *testing.T) {
	snapshots := inspector.NewService()
	snapshots.RecordSlots("allowed", "turn", []inspector.SlotSnapshot{{Name: "system", Tokens: 10, Content: "sensitive instructions", Sensitive: true}})
	sessions := &querySessionFixture{}
	reader := NewPluginQueryService(nil, sessions, nil, snapshots)
	scope := pluginapi.QueryScope{Resources: []pluginapi.QueryResource{pluginapi.QueryContextSlots}, SessionIDs: []string{"allowed"}}
	request := pluginapi.QueryRequest{Resource: pluginapi.QueryContextSlots, SessionID: "allowed"}
	raw, err := reader.Read(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	if slots := raw.(pluginapi.QuerySlotsData); !slots.Available || slots.Slots[0].Content != "" || !slots.Slots[0].Sensitive {
		t.Fatal("content released without approval")
	}
	scope.IncludeContent = true
	raw, err = reader.Read(context.Background(), scope, request)
	if err != nil {
		t.Fatal(err)
	}
	slots := raw.(pluginapi.QuerySlotsData)
	if slots.Slots[0].Content != "sensitive instructions" {
		t.Fatal("approved content missing")
	}
	slots.Slots[0].Content = "changed by consumer"
	if snapshots.Snapshot("allowed", "turn").Slots[0].Content != "sensitive instructions" {
		t.Fatal("read modified capture")
	}
	reader.inspector = nil
	raw, err = reader.Read(context.Background(), scope, request)
	if err != nil || raw.(pluginapi.QuerySlotsData).Available {
		t.Fatal("disabled inspector fabricated context")
	}
	reader.inspector = inspector.NewService()
	raw, err = reader.Read(context.Background(), scope, request)
	if err != nil || raw.(pluginapi.QuerySlotsData).Available {
		t.Fatal("missing capture fabricated context")
	}
	before := sessions.calls
	request.SessionID = "outside"
	if _, checkErr := reader.Read(context.Background(), scope, request); !errors.Is(checkErr, ErrPluginQueryDenied) || sessions.calls != before {
		t.Fatal("denied query reached session reader")
	}
	request.SessionID = "allowed"
	request.Resource = "sql"
	if _, checkErr := reader.Read(context.Background(), scope, request); !errors.Is(checkErr, ErrPluginQueryDenied) || sessions.calls != before {
		t.Fatal("unknown query reached session reader")
	}
	request.Resource = pluginapi.QueryContextSlots
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, checkErr := reader.Read(canceled, scope, request); !errors.Is(checkErr, context.Canceled) || sessions.calls != before {
		t.Fatal("canceled query reached session reader")
	}
	scope.SessionIDs = []string{"missing"}
	request.SessionID = "missing"
	if _, checkErr := reader.Read(context.Background(), scope, request); !errors.Is(checkErr, ErrPluginQueryNotFound) {
		t.Fatal("missing session not mapped")
	}
}
