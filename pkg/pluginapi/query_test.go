package pluginapi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func queryGrant(host string) pluginapi.QueryGrant {
	return pluginapi.QueryGrant{Protocol: 1, PluginID: "example.plugin", HostURL: host,
		Token: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("q", 32))),
		Scope: pluginapi.QueryScope{Resources: []pluginapi.QueryResource{pluginapi.QuerySessions, pluginapi.QueryUsage}, SessionIDs: []string{"session-a"}}}
}

func TestQueryScopeStrictNarrowing(t *testing.T) {
	valid := `{"resources":["sessions","context_slots"],"session_ids":["session-a"],"include_content":true}`
	scope, err := pluginapi.DecodeQueryScope(json.RawMessage(valid))
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Allows(pluginapi.QuerySessions, "") || !scope.Allows(pluginapi.QueryContextSlots, "session-a") || scope.Allows(pluginapi.QueryContextSlots, "") || scope.Allows(pluginapi.QuerySessions, "session-b") || scope.Allows(pluginapi.QueryUsage, "session-a") {
		t.Fatal("scope was expanded")
	}
	for _, raw := range []string{
		`{}`, `null`, `{"resources":["usage"],"all_sessions":false}`,
		`{"resources":["usage"],"session_ids":["session-a"],"all_sessions":true}`,
		`{"resources":["arbitrary_sql"],"all_sessions":true}`,
		`{"resources":["usage","usage"],"all_sessions":true}`,
		`{"resources":["usage"],"session_ids":["a","a"]}`,
		`{"resources":["usage"],"session_ids":["../a"]}`,
		`{"resources":["usage"],"all_sessions":true,"include_content":true}`,
		`{"resources":["usage"],"all_sessions":true,"all_sessions":false}`,
		`{"Resources":["usage"],"all_sessions":true}`,
		`{"resources":["usage"],"all_sessions":true,"sql":"SELECT 1"}`,
		valid + `{}`, strings.Repeat(" ", 16385) + valid,
	} {
		if _, decodeErr := pluginapi.DecodeQueryScope(json.RawMessage(raw)); decodeErr == nil {
			t.Errorf("accepted malformed scope %s", raw[:min(len(raw), 100)])
		}
	}
}

func TestQueryIdentityAndLoopbackBoundaries(t *testing.T) {
	grant := queryGrant("http://127.0.0.1:8090")
	raw, err := json.Marshal(map[string]any{"nanite_host_query": grant})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := pluginapi.QueryGrantFromIdentity(raw)
	if err != nil || parsed.PluginID != grant.PluginID || parsed.Token != grant.Token {
		t.Fatalf("grant identity: %v", err)
	}
	if _, missingErr := pluginapi.QueryGrantFromIdentity(nil); !errors.Is(missingErr, pluginapi.ErrQueryNotGranted) {
		t.Fatal(missingErr)
	}
	if _, missingErr := pluginapi.QueryGrantFromIdentity(json.RawMessage(`{}`)); !errors.Is(missingErr, pluginapi.ErrQueryNotGranted) {
		t.Fatal(missingErr)
	}
	for _, host := range []string{"http://localhost:8090", "http://example.com", "file:///tmp/socket", "http://127.0.0.1/api", "http://user:password@127.0.0.1", "http://127.0.0.1?token=x", "http://127.0.0.1#fragment"} {
		bad := grant
		bad.HostURL = host
		if _, clientErr := pluginapi.NewQueryClient(bad, nil); clientErr == nil {
			t.Errorf("accepted host %s", host)
		}
	}
	for _, invalid := range []string{`{"nanite_host_query":null}`, `{"nanite_host_query":{},"nanite_host_query":{}}`, strings.Replace(string(raw), `"protocol":1`, `"protocol":2`, 1), strings.Replace(string(raw), `"token":`, `"Token":`, 1)} {
		if _, identityErr := pluginapi.QueryGrantFromIdentity(json.RawMessage(invalid)); identityErr == nil {
			t.Fatal("accepted invalid identity")
		}
	}
}

func TestQueryClientWireScopeAndCopiedGrant(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/plugin-host/query/usage" || r.URL.Query().Get("session_id") != "session-a" || r.URL.Query().Get("limit") != "20" || r.Header.Get("Authorization") != "Bearer "+queryGrant("").Token {
			t.Error("wrong scoped query wire")
		}
		_, _ = w.Write([]byte(`{"protocol":1,"resource":"usage","session_id":"session-a","data":{"total_tokens":12}}`))
	}))
	defer server.Close()
	grant := queryGrant(server.URL)
	client, err := pluginapi.NewQueryClient(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	grant.Scope.Resources[1] = pluginapi.QueryExecutionMetrics
	grant.Scope.SessionIDs[0] = "session-b"
	result, err := client.Query(context.Background(), pluginapi.QueryRequest{Resource: pluginapi.QueryUsage, SessionID: "session-a", Limit: 20})
	if err != nil || string(result.Data) != `{"total_tokens":12}` {
		t.Fatalf("query result: %v %s", err, result.Data)
	}
	for _, query := range []pluginapi.QueryRequest{
		{Resource: pluginapi.QueryUsage, SessionID: "session-b"},
		{Resource: pluginapi.QueryExecutionMetrics, SessionID: "session-a"},
		{Resource: pluginapi.QueryUsage}, {Resource: "../../tools/call", SessionID: "session-a"},
		{Resource: pluginapi.QueryUsage, SessionID: "session-a", Limit: 101},
		{Resource: pluginapi.QueryUsage, SessionID: "session-a", Limit: -1},
	} {
		if _, queryErr := client.Query(context.Background(), query); queryErr == nil {
			t.Fatal("accepted expanded query")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid queries reached the host")
	}
}

func TestQueryClientRefusesRedirectsAndSecretErrorBodies(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	for _, redirect := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirect {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(queryGrant("").Token))
		}))
		client, err := pluginapi.NewQueryClient(queryGrant(server.URL), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Query(context.Background(), pluginapi.QueryRequest{Resource: pluginapi.QueryUsage, SessionID: "session-a"})
		server.Close()
		if err == nil || strings.Contains(err.Error(), queryGrant("").Token) {
			t.Fatal("redirect accepted or error body leaked")
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("query credential followed a redirect")
	}
}

func TestQueryClientRejectsOversizedAndMismatchedResponses(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("x", pluginapi.MaxQueryResponseBytes+1),
		`{"protocol":2,"resource":"usage","session_id":"session-a","data":{}}`,
		`{"protocol":1,"resource":"sessions","session_id":"session-a","data":{}}`,
		`{"protocol":1,"resource":"usage","session_id":"session-b","data":{}}`,
		`{"protocol":1,"resource":"usage","session_id":"session-a"}`,
		`{"protocol":1,"protocol":1,"resource":"usage","session_id":"session-a","data":{}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		client, err := pluginapi.NewQueryClient(queryGrant(server.URL), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Query(context.Background(), pluginapi.QueryRequest{Resource: pluginapi.QueryUsage, SessionID: "session-a"})
		server.Close()
		if err == nil {
			t.Fatal("accepted invalid response")
		}
	}
}

func TestQueryClientCancellationStopsRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := pluginapi.NewQueryClient(queryGrant(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Query(ctx, pluginapi.QueryRequest{Resource: pluginapi.QueryUsage, SessionID: "session-a"})
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("canceled query: %v (%d calls)", err, calls.Load())
	}
}
