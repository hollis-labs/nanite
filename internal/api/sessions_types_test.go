package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// sessionViewKeys is the wire contract of SessionView when every field is
// set. halted_at, halted_reason and runtime_state drop out when unset; the
// rest are always present. ui/src/lib/types.ts Session reads most of them.
var sessionViewKeys = []string{
	"id", "short_code", "title", "custom_name", "project_id", "context_type",
	"context_id", "provider", "model", "status", "is_pinned", "sort_order",
	"message_count", "tags", "metadata", "last_activity", "created_at",
	"updated_at", "halted_at", "halted_reason", "runtime_state",
	"parent_session_id", "root_session_id", "relation", "depth",
}

// messageViewKeys is the wire contract of MessageView.
var messageViewKeys = []string{
	"id", "session_id", "agent_id", "role", "content", "envelope", "metadata",
	"parent_id", "is_compacted", "created_at",
}

// populate sets every field of the struct v points at to a distinct
// non-zero value, allocating pointer fields, so a translator that drops or
// swaps a field shows up as a mismatch rather than two matching zeros.
func populate(t *testing.T, v any) {
	t.Helper()
	rv := reflect.ValueOf(v).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		if f.Kind() == reflect.Pointer {
			f.Set(reflect.New(f.Type().Elem()))
			f = f.Elem()
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString(fmt.Sprintf("value-%d", i))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int:
			f.SetInt(int64(1000 + i))
		default:
			t.Fatalf("%s.%s has kind %s; teach populate about it", rv.Type().Name(), rv.Type().Field(i).Name, f.Kind())
		}
	}
}

func sortedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return raw
}

func TestSessionViewJSON(t *testing.T) {
	var full store.Session
	populate(t, &full)
	got := mustJSON(t, sessionToView(&full))

	want := append([]string(nil), sessionViewKeys...)
	sort.Strings(want)
	if keys := sortedKeys(t, got); !reflect.DeepEqual(keys, want) {
		t.Fatalf("SessionView JSON keys changed\n got: %v\nwant: %v", keys, want)
	}

	// The view must encode exactly as the row did when handlers returned it:
	// populated, and with every pointer nil (omitted vs null).
	for name, row := range map[string]store.Session{"populated": full, "zero": {}} {
		row := row
		if got, want := mustJSON(t, sessionToView(&row)), mustJSON(t, row); string(got) != string(want) {
			t.Errorf("%s session: view JSON differs from row JSON\n got: %s\nwant: %s", name, got, want)
		}
	}

	zero := mustJSON(t, sessionToView(&store.Session{}))
	var m map[string]any
	if err := json.Unmarshal(zero, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"halted_at", "halted_reason", "runtime_state"} {
		if _, ok := m[k]; ok {
			t.Errorf("unset %s is present; want it omitted", k)
		}
	}
	for _, k := range []string{"parent_session_id", "root_session_id", "relation", "depth"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("unset %s = %#v (present=%v); want null", k, v, ok)
		}
	}
}

func TestMessageViewJSON(t *testing.T) {
	var msg store.Message
	populate(t, &msg)
	rows := []store.Message{msg}
	got := mustJSON(t, messagesToView(rows))

	want := append([]string(nil), messageViewKeys...)
	sort.Strings(want)
	var one []json.RawMessage
	if err := json.Unmarshal(got, &one); err != nil || len(one) != 1 {
		t.Fatalf("unmarshal message list: %v (len %d)", err, len(one))
	}
	if keys := sortedKeys(t, one[0]); !reflect.DeepEqual(keys, want) {
		t.Fatalf("MessageView JSON keys changed\n got: %v\nwant: %v", keys, want)
	}
	if want := mustJSON(t, rows); string(got) != string(want) {
		t.Fatalf("message view JSON differs from row JSON\n got: %s\nwant: %s", got, want)
	}

	// Empty stays [], nil stays null, as the store returned them.
	if got := string(mustJSON(t, messagesToView([]store.Message{}))); got != "[]" {
		t.Errorf("empty messages = %s, want []", got)
	}
	if got := string(mustJSON(t, messagesToView(nil))); got != "null" {
		t.Errorf("nil messages = %s, want null", got)
	}

	page := &store.MessagePage{Messages: rows, Total: 7, HasMore: true}
	if got, want := mustJSON(t, messagePageToView(page)), mustJSON(t, page); string(got) != string(want) {
		t.Fatalf("message page view JSON differs from row JSON\n got: %s\nwant: %s", got, want)
	}
}

// TestSessionDetailViewMatchesMapEncoding pins GET /api/sessions/{id}
// against the map[string]any the handler used to build from store types.
func TestSessionDetailViewMatchesMapEncoding(t *testing.T) {
	var sess store.Session
	populate(t, &sess)
	var msg store.Message
	populate(t, &msg)

	cases := map[string]struct {
		messages    []store.Message
		interrupted map[string]any
		active      string
	}{
		"interrupted": {
			messages:    []store.Message{msg},
			interrupted: map[string]any{"interrupted": true, "reason": "agent_gone", "last_message_id": "m1"},
			active:      "",
		},
		"streaming, no messages": {
			messages:    []store.Message{},
			interrupted: nil,
			active:      "msg-live",
		},
	}
	for name, tc := range cases {
		old := map[string]any{
			"session":           &sess,
			"messages":          tc.messages,
			"interrupted_turn":  tc.interrupted,
			"active_message_id": tc.active,
		}
		view := SessionDetailView{
			Session:         sessionToView(&sess),
			Messages:        messagesToView(tc.messages),
			InterruptedTurn: tc.interrupted,
			ActiveMessageID: tc.active,
		}
		if got, want := mustJSON(t, view), mustJSON(t, old); string(got) != string(want) {
			t.Errorf("%s: detail view JSON differs from the old map encoding\n got: %s\nwant: %s", name, got, want)
		}
	}
}
