package pluginapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func dataSnapshot() pluginapi.DataSnapshot {
	return pluginapi.DataSnapshot{Protocol: 1, PluginID: "hollis.bookmarks", Feature: "bookmarks", SourceID: "workspace-one", Columns: []string{"id", "note", "large", "number", "payload", "empty", "invalid"}, Rows: [][]pluginapi.DataCell{{{Kind: "text", Text: "id-one"}, {Kind: "text", Text: "hello\\n世界"}, {Kind: "integer", Text: "9223372036854775807"}, {Kind: "real", Text: "1.2345678901234567"}, {Kind: "blob", Text: "AAH/"}, {Kind: "null"}, {Kind: "text_bytes", Text: "/w=="}}}}
}

func TestDataExportRoundTripAndReceipt(t *testing.T) {
	snapshot := dataSnapshot()
	raw, err := pluginapi.EncodeDataExport(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := pluginapi.DecodeDataExport(bytes.NewReader(raw))
	if err != nil || !reflect.DeepEqual(decoded.Snapshot, snapshot) {
		t.Fatalf("round trip: %+v %v", decoded, err)
	}
	value, err := decoded.Snapshot.Rows[0][2].Value()
	if err != nil || value != int64(9223372036854775807) {
		t.Fatal("integer precision lost")
	}
	value, err = decoded.Snapshot.Rows[0][6].Value()
	if err != nil || value != string([]byte{255}) {
		t.Fatal("SQLite text bytes lost")
	}
	receipt := pluginapi.DataExportReceipt{PluginID: snapshot.PluginID, Feature: snapshot.Feature, SourceID: snapshot.SourceID, Path: "core-imports/workspace-one/bookmarks.jsonl", SHA256: decoded.SHA256, RowCount: 1}
	if err := receipt.Verify(decoded); err != nil {
		t.Fatal(err)
	}
	receipt.SourceID = "other-workspace"
	if receipt.Verify(decoded) == nil {
		t.Fatal("accepted wrong workspace receipt")
	}
	tampered := bytes.Replace(raw, []byte("hello"), []byte("other"), 1)
	if _, err := pluginapi.DecodeDataExport(bytes.NewReader(tampered)); err == nil {
		t.Fatal("checksum accepted tampered rows")
	}
	for _, change := range []func(*pluginapi.DataSnapshot){
		func(s *pluginapi.DataSnapshot) { s.Rows[0] = s.Rows[0][:2] },
		func(s *pluginapi.DataSnapshot) { s.Columns[1] = s.Columns[0] },
		func(s *pluginapi.DataSnapshot) { s.Rows[0][2].Text = "9223372036854775808" },
		func(s *pluginapi.DataSnapshot) { s.Rows[0][3].Text = "NaN" },
		func(s *pluginapi.DataSnapshot) { s.Rows[0][5].Text = "nonempty" },
	} {
		bad := dataSnapshot()
		change(&bad)
		if _, err := pluginapi.EncodeDataExport(bad); err == nil {
			t.Fatal("accepted invalid data")
		}
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"protocol":1`), []byte(`"protocol":1,"protocol":1`), 1), bytes.Replace(raw, []byte(`"kind":"text"`), []byte(`"Kind":"text"`), 1), bytes.Replace(raw, []byte(`"row_count":1`), []byte(`"row_count":0`), 1), bytes.Split(raw, []byte("\n"))[0]} {
		if _, err := pluginapi.DecodeDataExport(bytes.NewReader(bad)); err == nil {
			t.Fatal("accepted ambiguous or truncated export")
		}
	}
}

func TestDataExportLargerThanManifest(t *testing.T) {
	snapshot := dataSnapshot()
	snapshot.Rows = make([][]pluginapi.DataCell, 20)
	for index := range snapshot.Rows {
		snapshot.Rows[index] = dataSnapshot().Rows[0]
		snapshot.Rows[index][1].Text = strings.Repeat("x", 100000)
	}
	raw, err := pluginapi.EncodeDataExport(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 1<<20 {
		t.Fatal("fixture did not exceed manifest cap")
	}
	decoded, err := pluginapi.DecodeDataExport(bytes.NewReader(raw))
	if err != nil || len(decoded.Snapshot.Rows) != 20 {
		t.Fatalf("large valid export: %v", err)
	}
	snapshot.Rows[0][1].Text = strings.Repeat("x", 1<<20)
	if _, err := pluginapi.EncodeDataExport(snapshot); err == nil {
		t.Fatal("accepted oversized row")
	}
}

func TestResolveCoreReferenceScopedWire(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/plugin-host/query/message_refs" || r.URL.Query().Get("session_id") != "session-a" || r.URL.Query().Get("message_id") != "message-a" {
			t.Error("wrong reference query")
		}
		data, _ := json.Marshal(pluginapi.QueryMessageReferencesData{References: []pluginapi.QueryMessageReference{{SessionID: "session-a", MessageID: "message-a", Role: "user"}}})
		_ = json.NewEncoder(w).Encode(pluginapi.QueryResponse{Protocol: 1, Resource: pluginapi.QueryMessageReferences, SessionID: "session-a", Data: data})
	}))
	defer server.Close()
	grant := queryGrant(server.URL)
	grant.Scope.Resources = []pluginapi.QueryResource{pluginapi.QueryMessageReferences}
	client, err := pluginapi.NewQueryClient(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := client.ResolveReference(context.Background(), pluginapi.CoreReference{SessionID: "session-a", MessageID: "message-a"})
	if err != nil || reference.MessageID != "message-a" || calls != 1 {
		t.Fatalf("reference: %+v %v", reference, err)
	}
	if _, err := client.ResolveReference(context.Background(), pluginapi.CoreReference{SessionID: "other", MessageID: "message-a"}); err == nil || calls != 1 {
		t.Fatal("scope widened before RPC")
	}
}
