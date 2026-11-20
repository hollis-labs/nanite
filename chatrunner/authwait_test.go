package chatrunner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
)

// fakeWarden refuses llm-gateway tokens until its request is approved.
type fakeWarden struct {
	srv      *httptest.Server
	mu       sync.Mutex
	status   string // the grant request's status
	filed    int
	lookups  atomic.Int32
	purposes []string
}

func newFakeWarden(t *testing.T) *fakeWarden {
	t.Helper()
	f := &fakeWarden{status: "pending"}
	next := atomic.Int64{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/token":
			_ = r.ParseForm()
			f.mu.Lock()
			granted := f.status == "approved"
			f.mu.Unlock()
			if r.Form.Get("audience") == "llm-gateway" && !granted {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_scope"}`)
				return
			}
			n := next.Add(1)
			// Values built at run time, so the source holds no
			// credential-shaped literal.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access" + "_token": fmt.Sprint("fixture-a-", n), "expires_in": 900,
				"refresh" + "_token": fmt.Sprint("fixture-p-", n),
			})
		case r.URL.Path == "/api/v1/requests" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.filed++
			f.purposes = append(f.purposes, string(body))
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"id":"req-1","status":"pending","tier":"critical"}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/requests/"):
			f.lookups.Add(1)
			f.mu.Lock()
			status := f.status
			f.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":"req-1","status":%q}`, status)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeWarden) set(status string) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

func (f *fakeWarden) requestBodies() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.purposes, "\n")
}

// gatewayLLM answers every chat request with one line of text.
func gatewayLLM(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"granted and answering"},"finish_reason":""}]}`,
			`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// wardenAgentDir writes providers.yaml and identity.yaml so the agent's chat
// model is reached with Warden tokens.
func wardenAgentDir(t *testing.T, dir string, ws *fakeWarden, llm *httptest.Server) {
	t.Helper()
	cred := filepath.Join(dir+"-cred", "agent.cred")
	if err := os.MkdirAll(filepath.Dir(cred), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cred); err != nil {
		if err := tokenfile.Write(cred, "fixture-parent-0"); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"providers.yaml": "providers:\n  gateway:\n    wire: openai-chat\n    base_url: " + llm.URL + "/v1\n    auth: warden\n    audience: llm-gateway\n    scopes: [llm-gateway.chat]\nuse:\n  chat: {provider: gateway, model: m}\n",
		"identity.yaml":  "principal: agent:chat-test\nwarden:\n  url: " + ws.srv.URL + "\n  credential_file: " + cred + "\n",
	}
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func wardenRunner(t *testing.T, dir string, ws *fakeWarden, llm *httptest.Server, status chatrunner.AuthStatus) *wired {
	t.Helper()
	wardenAgentDir(t, dir, ws, llm)
	return newWiredIn(t, dir, false, func(o *chatrunner.Options) {
		o.Agent.Provider = nil
		o.NoticeTurns = true
		o.AuthPoll = 20 * time.Millisecond
		o.AuthStatus = status
	})
}

func isAuthWaiting(ev chatstream.Event) bool {
	return ev.Verb == chatstream.VerbActivity && ev.Kind == agent.ActivityAuthWaiting
}

// A turn that needs a grant the agent lacks ends with a clear message, the
// chat shows the request, and once Warden approves it a notice turn retries
// and the model answers.
func TestAuthWaitingShowsInTheChatAndRetriesOnApproval(t *testing.T) {
	ws := newFakeWarden(t)
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), nil)

	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	var failed chatstream.Event
	w.until(t, "the turn's end", func(ev chatstream.Event) bool {
		if ev.IsTerminal() {
			failed = ev
			return true
		}
		return false
	})
	if failed.Verb != chatstream.VerbRunError || !strings.Contains(failed.Message, "Waiting for approval of Warden grant request req-1") ||
		!strings.Contains(failed.Message, "provider gateway") {
		t.Fatalf("turn ended with %+v", failed)
	}
	var waiting chatstream.Event
	w.until(t, "the waiting activity", func(ev chatstream.Event) bool {
		if isAuthWaiting(ev) {
			waiting = ev
			return true
		}
		return false
	})
	v := activityValue(t, waiting)
	if v["request_id"] != "req-1" || v["audience"] != "llm-gateway" || v["for"] != "provider gateway" {
		t.Fatalf("waiting activity %v", v)
	}
	if body := ws.requestBodies(); !strings.Contains(body, "provider gateway") || !strings.Contains(body, chatrunner.ConversationID(w.conv)) {
		t.Fatalf("grant request purpose does not name what needed it and where: %s", body)
	}

	ws.set("approved")
	w.until(t, "the retried answer", func(ev chatstream.Event) bool {
		return answered(ev)
	})
}

// A denial is told to the chat and nothing retries.
func TestAuthWaitingDenialIsTold(t *testing.T) {
	ws := newFakeWarden(t)
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), nil)
	if _, err := w.svc.Submit(t.Context(), w.conv, chat.Turn{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	w.until(t, "the waiting activity", isAuthWaiting)
	ws.set("denied")
	w.until(t, "the denial notice", func(ev chatstream.Event) bool {
		if ev.Verb != chatstream.VerbActivity || ev.Kind != chatrunner.ActivityNotice {
			return false
		}
		v := activityValue(t, ev)
		body, _ := v["body"].(string)
		return v["kind"] == chatrunner.NoticeKindAuth && strings.Contains(body, "req-1") && strings.Contains(body, "was denied")
	})
}

// A restart keeps watching the request and still retries on approval.
func TestAuthWaitingSurvivesARestart(t *testing.T) {
	ws := newFakeWarden(t)
	llm := gatewayLLM(t)
	dir := t.TempDir()
	first := wardenRunner(t, dir, ws, llm, func(context.Context, string) (string, error) { return "pending", nil })
	if _, err := first.svc.Submit(t.Context(), first.conv, chat.Turn{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	first.until(t, "the waiting activity", isAuthWaiting)
	if err := first.runner.Close(); err != nil {
		t.Fatal(err)
	}
	_ = first.svc.Close(t.Context())

	ws.set("approved")
	before := ws.lookups.Load()
	second := wardenRunner(t, dir, ws, llm, nil)
	if second.conv != first.conv {
		t.Fatalf("restarted chat is %s, want %s", second.conv, first.conv)
	}
	second.until(t, "the retried answer after the restart", func(ev chatstream.Event) bool {
		return answered(ev)
	})
	if ws.lookups.Load() == before {
		t.Fatal("the restarted runner never looked the request up")
	}
}

// answered reports whether ev carries the gateway model's answer.
func answered(ev chatstream.Event) bool {
	raw, _ := json.Marshal(ev)
	return strings.Contains(string(raw), "granted and answering")
}
