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
	"sync/atomic"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/chatrunner"
	"github.com/hollis-labs/nanite/internal/warden/wardentest"
)

// fakeWarden is the shared fake Warden (wardentest) with no llm-gateway
// grant: a token for it files a critical grant request, which set decides.
type fakeWarden struct {
	*wardentest.Fake
	srv *httptest.Server
}

func newFakeWarden(t *testing.T) *fakeWarden {
	t.Helper()
	f := wardentest.NewFake(t, "fixture-parent-0").FileAs("pending", "critical").
		// Values built at run time, so the source holds no credential-shaped
		// literal.
		Tokens(func(n int, aud string) string { return fmt.Sprint("fixture-a-", aud, "-", n) }).
		Parents(func(n int) string { return fmt.Sprint("fixture-p-", n) })
	return &fakeWarden{Fake: f, srv: f.Server}
}

// set decides the latest grant request: "approved" or "denied"; "gone"
// removes it (its lookups answer 404); "error" makes lookups fail (502)
// until another status; "pending" leaves it waiting.
func (f *fakeWarden) set(status string) {
	f.Mend("/api/v1/requests/")
	ids := f.Requests()
	latest := ""
	if len(ids) > 0 {
		latest = ids[len(ids)-1]
	}
	switch status {
	case "approved", "denied":
		if latest != "" {
			f.Decide(latest, status)
		}
		if status == "approved" {
			f.Grant("llm-gateway")
		}
	case "gone":
		if latest != "" {
			f.Remove(latest)
		}
	case "error":
		f.Break("/api/v1/requests/", http.StatusBadGateway)
	}
}

// requests is how many grant requests were filed.
func (f *fakeWarden) requests() int { return len(f.Requests()) }

// requestBodies are the filed grant requests' bodies.
func (f *fakeWarden) requestBodies() string {
	var out []string
	for _, body := range f.Filed() {
		raw, _ := json.Marshal(body)
		out = append(out, string(raw))
	}
	return strings.Join(out, "\n")
}

// lookups counts grant-request lookups.
func (f *fakeWarden) lookups() int {
	n := 0
	for _, id := range f.Requests() {
		n += f.Calls("/api/v1/requests/" + id)
	}
	return n
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

func wardenRunner(t *testing.T, dir string, ws *fakeWarden, llm *httptest.Server, tweak func(*chatrunner.Options)) *wired {
	t.Helper()
	wardenAgentDir(t, dir, ws, llm)
	return newWiredIn(t, dir, false, func(o *chatrunner.Options) {
		o.Agent.Provider = nil
		o.NoticeTurns = true
		if tweak != nil {
			tweak(o)
		}
	})
}

// waitForGrant submits a message that needs the missing grant and returns
// the turn's run error once the chat shows the waiting activity.
func (w *wired) waitForGrant(t *testing.T) chatstream.Event {
	t.Helper()
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
	w.until(t, "the waiting activity", isAuthWaiting)
	return failed
}

// authNotice waits for an auth notice whose body holds every text in has.
func (w *wired) authNotice(t *testing.T, what string, has ...string) string {
	t.Helper()
	var body string
	w.until(t, what, func(ev chatstream.Event) bool {
		if ev.Verb != chatstream.VerbActivity || ev.Kind != chatrunner.ActivityNotice {
			return false
		}
		v := activityValue(t, ev)
		body, _ = v["body"].(string)
		if v["kind"] != chatrunner.NoticeKindAuth {
			return false
		}
		for _, s := range has {
			if !strings.Contains(body, s) {
				return false
			}
		}
		return true
	})
	return body
}

func pending(context.Context, string) (string, error) { return "pending", nil }

func isAuthWaiting(ev chatstream.Event) bool {
	return ev.Verb == chatstream.VerbActivity && ev.Kind == agent.ActivityAuthWaiting
}

// A turn that needs a grant the agent lacks ends with a clear message, the
// chat shows the request, and once Warden approves it a notice turn retries
// and the model answers.
func TestAuthWaitingShowsInTheChatAndRetriesOnApproval(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
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
		!strings.Contains(failed.Message, "provider gateway") || !strings.Contains(failed.Message, "continues once it is approved") {
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
	if !clock.Tick() {
		t.Fatal("no watcher polled the request")
	}
	w.authNotice(t, "the approval notice", "approved grant request req-1", "Continue the work")
	w.until(t, "the retried answer", answered)
}

// A denial is told to the chat, and the next attempt files a new request
// instead of reusing the denied one.
func TestAuthWaitingDenialIsToldAndANewRequestIsFiled(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), nil)
	w.waitForGrant(t)
	ws.set("denied")
	if !clock.Tick() {
		t.Fatal("no watcher polled the request")
	}
	w.authNotice(t, "the denial notice", "req-1", "was denied")
	ws.set("pending")
	// The denial notice's turn (or the owner's next message) tries again.
	waitFor(t, "a new grant request", func() bool { return ws.requests() >= 2 })
}

// A restart keeps watching the request and still retries on approval.
func TestAuthWaitingSurvivesARestart(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	llm := gatewayLLM(t)
	dir := t.TempDir()
	first := wardenRunner(t, dir, ws, llm, func(o *chatrunner.Options) { o.AuthStatus = pending })
	first.waitForGrant(t)
	if err := first.runner.Close(); err != nil {
		t.Fatal(err)
	}
	_ = first.svc.Close(t.Context())

	ws.set("approved")
	before := ws.lookups()
	second := wardenRunner(t, dir, ws, llm, nil)
	if second.conv != first.conv {
		t.Fatalf("restarted chat is %s, want %s", second.conv, first.conv)
	}
	if !clock.Tick() {
		t.Fatal("the restarted runner does not watch the request")
	}
	second.until(t, "the retried answer after the restart", answered)
	if ws.lookups() == before {
		t.Fatal("the restarted runner never looked the request up")
	}
}

// A request Warden no longer shows is given up at once, and the chat is
// told.
func TestAuthWaitingStopsWhenTheRequestIsGone(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	// No notice turns: the stop notice must not start a turn that files a
	// new request (and a new watcher) for the check below.
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), func(o *chatrunner.Options) { o.NoticeTurns = false })
	if failed := w.waitForGrant(t); !strings.Contains(failed.Message, "You will get a notice when it is decided") {
		t.Fatalf("without notice turns the turn ended with %q", failed.Message)
	}
	ws.set("gone")
	if !clock.Tick() {
		t.Fatal("no watcher polled the request")
	}
	w.authNotice(t, "the stop notice", "Stopped watching Warden grant request req-1", "404")
	if clock.Tick() {
		t.Fatal("the watcher went on after the request was gone")
	}
}

// A request with no decision is given up after AuthWaitMax.
func TestAuthWaitingStopsAfterTheMaximumWait(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), func(o *chatrunner.Options) {
		o.AuthStatus = pending
		o.AuthWaitMax = time.Hour
	})
	w.waitForGrant(t)
	if !clock.Tick() {
		t.Fatal("no watcher polled the request")
	}
	clock.Advance(2 * time.Hour)
	if !clock.Tick() {
		t.Fatal("the watcher stopped too early")
	}
	w.authNotice(t, "the stop notice", "Stopped watching Warden grant request req-1", "no decision after 1h0m0s")
}

// Lookup errors keep the watcher going; the decision still arrives.
func TestAuthWaitingRidesOutLookupErrors(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), nil)
	w.waitForGrant(t)
	ws.set("error")
	for range 3 {
		before := ws.lookups()
		if !clock.Tick() {
			t.Fatal("the watcher stopped on a lookup error")
		}
		// Let the lookup finish before the next change.
		waitFor(t, "the lookup", func() bool { return ws.lookups() > before })
	}
	ws.set("approved")
	if !clock.Tick() {
		t.Fatal("the watcher stopped on a lookup error")
	}
	w.until(t, "the retried answer", answered)
}

// A pushed decision racing the watcher's own poll tells the chat once.
func TestAuthWaitingPushRacingThePollTellsOnce(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	release := make(chan struct{})
	polled := make(chan struct{}, 1)
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), func(o *chatrunner.Options) {
		o.AuthStatus = func(ctx context.Context, _ string) (string, error) {
			polled <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return " GRANTED ", nil
		}
	})
	w.waitForGrant(t)
	ws.set("approved")
	if !clock.Tick() {
		t.Fatal("no watcher polled the request")
	}
	<-polled
	pushed := make(chan error, 1)
	go func() { pushed <- w.runner.AuthDecided(t.Context(), "req-1", "approved") }()
	close(release)
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}
	w.until(t, "the retried answer", answered)
	// Read what else arrives for a moment: no second approval notice.
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	for {
		ev, err := w.stream.Next(ctx)
		if err != nil {
			break
		}
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice && activityValue(t, ev)["kind"] == chatrunner.NoticeKindAuth {
			t.Fatal("the chat was told about the approval twice")
		}
	}
}

// answered reports whether ev carries the gateway model's answer.
func answered(ev chatstream.Event) bool {
	raw, _ := json.Marshal(ev)
	return strings.Contains(string(raw), "granted and answering")
}

// A retry after a restart must still remove the chat from the watch. The
// source must remember that this conversation previously lacked the grant.
func TestAuthOwnerRetryAfterRestartSuppressesApproval(t *testing.T) {
	chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	llm := gatewayLLM(t)
	dir := t.TempDir()
	tweak := func(o *chatrunner.Options) { o.AuthStatus = pending }
	first := wardenRunner(t, dir, ws, llm, tweak)
	first.waitForGrant(t)
	waitFor(t, "saved auth watch", func() bool {
		raw, err := first.runner.Agent().HostState(t.Context(), "auth.waits")
		return err == nil && strings.Contains(raw, "req-1")
	})
	if err := first.runner.Agent().SetHostState(t.Context(), "auth.source-waiters", "host-written garbage"); err != nil {
		t.Fatal(err)
	}
	if err := first.runner.Close(); err != nil {
		t.Fatal(err)
	}
	_ = first.svc.Close(t.Context())
	ws.set("approved")
	second := wardenRunner(t, dir, ws, llm, tweak)
	if _, err := second.svc.Submit(t.Context(), second.conv, chat.Turn{Text: "try again"}); err != nil {
		t.Fatal(err)
	}
	events := second.until(t, "owner retry end", func(ev chatstream.Event) bool { return ev.IsTerminal() })
	sawAnswer := false
	for _, ev := range events {
		sawAnswer = sawAnswer || answered(ev)
	}
	if !sawAnswer {
		t.Fatal("owner retry did not answer")
	}
	waitFor(t, "cleared auth watch", func() bool {
		raw, err := second.runner.Agent().HostState(t.Context(), "auth.waits")
		return err == nil && raw == ""
	})
	raw, err := second.runner.Agent().HostState(t.Context(), "auth.source-waiters")
	if err != nil || raw != "host-written garbage" {
		t.Fatalf("source changed host-owned data: %q, %v", raw, err)
	}
	notices := second.runner.Agent().SubscribeNotices(t.Context())
	if err := second.runner.AuthDecided(t.Context(), "req-1", "approved"); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notices:
		t.Fatalf("successful owner retry got another notice: %+v", n)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAuthUnknownPollStatusKeepsWatching(t *testing.T) {
	clock := chatrunner.UseAuthClock(t)
	ws := newFakeWarden(t)
	var polls atomic.Int32
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), func(o *chatrunner.Options) {
		o.AuthStatus = func(context.Context, string) (string, error) {
			if polls.Add(1) == 1 {
				return "unexpected", nil
			}
			return " GRANTED ", nil
		}
	})
	w.waitForGrant(t)
	ws.set("approved")
	if !clock.Tick() {
		t.Fatal("no first poll")
	}
	if !clock.Tick() {
		t.Fatal("unknown status stopped the watch")
	}
	w.authNotice(t, "approval after unknown status", "approved grant request req-1")
}

func TestAutoApproveGrantFailureDoesNotHoldChat(t *testing.T) {
	ws := newFakeWarden(t)
	w := wardenRunner(t, t.TempDir(), ws, gatewayLLM(t), func(o *chatrunner.Options) { o.Agent.AutoApprove = true })
	seen := w.turn(t, "hi")
	var failed bool
	for _, ev := range seen {
		if isAuthWaiting(ev) {
			t.Fatal("auto mode emitted waiting activity")
		}
		if ev.Verb == chatstream.VerbRunError {
			failed = true
			if !strings.Contains(ev.Message, "llm-gateway") || !strings.Contains(ev.Message, "req-1") || strings.Contains(ev.Message, "continues once") {
				t.Fatalf("error %+v", ev)
			}
		}
	}
	if !failed {
		t.Fatal("no ordinary run error")
	}
	raw, err := w.runner.Agent().HostState(t.Context(), "auth.waits")
	if err != nil || raw != "" {
		t.Fatalf("persisted auth hold %q %v", raw, err)
	}
}
