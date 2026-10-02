package pluginapi

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
)

func wakeGrant(origin string) DurableWakeGrant {
	return DurableWakeGrant{Protocol: DurableWakeProtocol, PluginID: "nanite.loom", HostURL: origin, Token: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), Scope: DurableWakeScope{AgentSlugs: []string{"loom-curator"}}}
}
func TestDurableWakeScopeIdentityAndBounds(t *testing.T) {
	for _, raw := range []string{`{}`, `{"agent_slugs":[]}`, `{"agent_slugs":["loom-curator","loom-curator"]}`, `{"agent_slugs":["../curator"]}`, `{"agent_slugs":["loom-curator"],"anything":true}`, `{"agent_slugs":["loom-curator"],"agent_slugs":["other"]}`} {
		if _, err := DecodeDurableWakeScope([]byte(raw)); err == nil {
			t.Fatal("accepted invalid wake scope", raw)
		}
	}
	grant := wakeGrant("http://127.0.0.1:8090")
	query := QueryGrant{Protocol: QueryProtocol, PluginID: grant.PluginID, HostURL: grant.HostURL, Token: grant.Token, Scope: QueryScope{Resources: []QueryResource{QuerySessions}, AllSessions: true}}
	identity, err := json.Marshal(map[string]any{"nanite_durable_wake": grant, "nanite_host_query": query})
	if err != nil {
		t.Fatal(err)
	}
	if got, grantErr := DurableWakeGrantFromIdentity(identity); grantErr != nil || !got.Scope.Allows("loom-curator") || got.Scope.Allows("other") {
		t.Fatal(got, grantErr)
	}
	if _, err = QueryGrantFromIdentity(identity); err != nil {
		t.Fatal("combined identity refused", err)
	}
	if _, err = DurableWakeGrantFromIdentity(nil); !errors.Is(err, ErrDurableWakeNotGranted) {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://example.com", "http://localhost", "http://user:password@127.0.0.1", "http://127.0.0.1/path"} {
		bad := wakeGrant(origin)
		if bad.Validate() == nil {
			t.Fatal("accepted unsafe origin", origin)
		}
	}
	for _, call := range []DurableWakeRequest{{AgentSlug: "loom-curator"}, {AgentSlug: "../curator", Prompt: "wake"}, {AgentSlug: "loom-curator", Prompt: strings.Repeat("x", 32769)}, {AgentSlug: "loom-curator", Prompt: "wake", Facts: map[string]string{"fragment_id": strings.Repeat("x", 8193)}}, {AgentSlug: "loom-curator", Prompt: "wake\x00"}} {
		if call.Validate() == nil {
			t.Fatal("accepted invalid request")
		}
	}
}
func TestDurableWakeClientScopeAndResponse(t *testing.T) {
	var requests atomic.Int64
	var token string
	response := `{"protocol":1,"agent_slug":"loom-curator","instance_id":"instance-one","session_id":"session-one","status":"queued"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/plugin-host/durable-wake" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wire path or credential changed")
		}
		var call DurableWakeRequest
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil || call.AgentSlug != "loom-curator" || call.Prompt != "classify fragment" || call.Facts["fragment_id"] != "fragment-one" {
			t.Error("wake payload changed")
		}
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	grant := wakeGrant(server.URL)
	token = grant.Token
	client, err := NewDurableWakeClient(grant, nil)
	if err != nil {
		t.Fatal(err)
	}
	grant.Scope.AgentSlugs[0] = "other"
	call := DurableWakeRequest{AgentSlug: "loom-curator", Prompt: "classify fragment", Facts: map[string]string{"fragment_id": "fragment-one"}}
	if result, wakeErr := client.Wake(context.Background(), call); wakeErr != nil || result.SessionID != "session-one" {
		t.Fatal(result, wakeErr)
	}
	call.AgentSlug = "other"
	if _, err = client.Wake(context.Background(), call); err == nil || requests.Load() != 1 {
		t.Fatal("scope escaped copied grant")
	}
	call.AgentSlug = "loom-curator"
	for _, raw := range []string{strings.Repeat("x", MaxDurableWakeBytes+1), `{"protocol":1,"agent_slug":"other","instance_id":"i","status":"queued"}`, `{"protocol":1,"agent_slug":"loom-curator","instance_id":"i","status":"queued","unexpected":true}`, `{"protocol":1,"protocol":2,"agent_slug":"loom-curator","instance_id":"i","status":"queued"}`} {
		response = raw
		if _, err = client.Wake(context.Background(), call); err == nil {
			t.Fatal("accepted invalid wake response")
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	before := requests.Load()
	if _, err = client.Wake(canceled, call); !errors.Is(err, context.Canceled) || requests.Load() != before {
		t.Fatal("canceled wake sent")
	}
}
func TestDurableWakeDoesNotRedirectRetryOrLeakBody(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := NewDurableWakeClient(wakeGrant(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Wake(context.Background(), DurableWakeRequest{AgentSlug: "loom-curator", Prompt: "wake"}); err == nil || targetCalls.Load() != 0 || calls.Load() != 1 {
		t.Fatal("redirect or retry escaped")
	}
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("private-fragment-body"))
	}))
	defer secret.Close()
	client, err = NewDurableWakeClient(wakeGrant(secret.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Wake(context.Background(), DurableWakeRequest{AgentSlug: "loom-curator", Prompt: "wake"}); err == nil || strings.Contains(err.Error(), "private-fragment-body") {
		t.Fatal("response body leaked")
	}
}
