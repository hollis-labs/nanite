package tethermail_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/messaging"
	"github.com/hollis-labs/nanite/internal/tethermail"
	"github.com/hollis-labs/nanite/internal/tethermail/tethermailtest"
)

var me = messaging.Address{Kind: messaging.KindAgent, Authority: "agent-mux", ID: "me"}
var peer = messaging.Address{Kind: messaging.KindAgent, Authority: "agent-mux", ID: "peer"}

func mail(n int) messaging.Envelope {
	return messaging.Envelope{Kind: messaging.MsgKindRequest, From: peer, To: me, ThreadID: "T",
		Payload: []byte(fmt.Sprintf(`{"n":%d}`, n))}
}

func setup(t *testing.T, opts ...tethermail.Option) (*tethermailtest.Server, *tethermail.Client) {
	t.Helper()
	s := tethermailtest.NewServer(t)
	c, err := tethermail.New(s.URL, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func next(t *testing.T, ch <-chan tethermail.Event) tethermail.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
	return tethermail.Event{}
}

func TestListPaginationAndUnread(t *testing.T) {
	s, c := setup(t)
	for i := 1; i <= 5; i++ {
		s.Add(mail(i))
	}
	msgs, total, err := c.List(ctx(t), me.URN(), tethermail.ListOptions{Limit: 2, Offset: 2})
	if err != nil || total != 5 || len(msgs) != 2 || msgs[0].ID != "msg-3" {
		t.Fatalf("got %v total=%d err=%v", msgs, total, err)
	}
	if err := c.Consume(ctx(t), "msg-1", me.URN()); err != nil {
		t.Fatal(err)
	}
	msgs, total, _ = c.List(ctx(t), me.URN(), tethermail.ListOptions{UnreadOnly: true})
	if total != 4 || len(msgs) != 4 || msgs[0].ID != "msg-2" || !s.Consumed("msg-1") {
		t.Fatalf("unread: %d %v", total, msgs)
	}
}

func TestWatchFromNowLive(t *testing.T) {
	s, c := setup(t)
	s.Add(mail(1))
	ch, err := c.Watch(ctx(t), me.URN(), 0, tethermail.WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ev := next(t, ch); ev.Kind != tethermail.EventSynchronized {
		t.Fatalf("want synchronized, got %+v", ev)
	}
	seq := s.Add(mail(2))
	ev := next(t, ch)
	if ev.Kind != tethermail.EventMessage || ev.Seq != seq || ev.Env.ID != "msg-2" {
		t.Fatalf("got %+v", ev)
	}
}

func TestWatchAfterSeqReplay(t *testing.T) {
	s, c := setup(t)
	for i := 1; i <= 3; i++ {
		s.Add(mail(i))
	}
	ch, err := c.Watch(ctx(t), me.URN(), 1, tethermail.WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{2, 3} {
		if ev := next(t, ch); ev.Kind != tethermail.EventMessage || ev.Seq != want {
			t.Fatalf("want seq %d got %+v", want, ev)
		}
	}
	if ev := next(t, ch); ev.Kind != tethermail.EventSynchronized {
		t.Fatalf("got %+v", ev)
	}
}

func TestWatchGapAheadCloses(t *testing.T) {
	s, c := setup(t)
	s.Add(mail(1))
	ch, err := c.Watch(ctx(t), me.URN(), 99, tethermail.WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ev := next(t, ch); ev.Kind != tethermail.EventGap || ev.Reason != "ahead" {
		t.Fatalf("got %+v", ev)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not closed")
	}
}

func TestWatchDropClosesChannel(t *testing.T) {
	s, c := setup(t)
	ch, _ := c.Watch(ctx(t), me.URN(), 0, tethermail.WatchOptions{})
	next(t, ch)
	s.DropWatchers()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not closed")
	}
}

func TestSSEParserPingsAndMultilineData(t *testing.T) {
	big := strings.Repeat("x", 200000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": ping\n\n")
		// multi-line data: JSON split across lines; seq only in the body
		fmt.Fprint(w, "event: message\r\ndata: {\"id\":\"a\",\ndata: \"seq\":7,\"content_type\":\""+big+"\"}\n\n")
		fmt.Fprint(w, ": ping\n\nevent: synchronized\ndata: {}\n\n")
	}))
	defer srv.Close()
	c, _ := tethermail.New(srv.URL)
	ch, err := c.Watch(ctx(t), me.URN(), 0, tethermail.WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ev := next(t, ch)
	if ev.Kind != tethermail.EventMessage || ev.Seq != 7 || ev.Env.ID != "a" || len(ev.Env.ContentType) != len(big) {
		t.Fatalf("bad event kind=%s seq=%d id=%s", ev.Kind, ev.Seq, ev.Env.ID)
	}
	if ev := next(t, ch); ev.Kind != tethermail.EventSynchronized {
		t.Fatalf("got %+v", ev)
	}
}

func TestSendIdempotency(t *testing.T) {
	s, c := setup(t)
	a, err := c.Send(ctx(t), mail(1), "k1")
	if err != nil || a.ID == "" {
		t.Fatal(a, err)
	}
	b, err := c.Send(ctx(t), mail(1), "k1")
	if err != nil || b.ID != a.ID || len(s.Sent()) != 1 {
		t.Fatalf("replay: %v %v sent=%d", b, err, len(s.Sent()))
	}
	if _, err := c.Send(ctx(t), mail(1), "k2"); err != nil || len(s.Sent()) != 2 {
		t.Fatal(err)
	}
}

func TestUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "tm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	s := tethermailtest.NewServer(t)
	s.ListenUnix(sock)
	s.Add(mail(1))
	for _, addr := range []string{sock, "unix://" + sock} {
		c, err := tethermail.New(addr)
		if err != nil {
			t.Fatal(err)
		}
		if _, total, err := c.List(ctx(t), me.URN(), tethermail.ListOptions{}); err != nil || total != 1 {
			t.Fatalf("%s: total=%d err=%v", addr, total, err)
		}
	}
}

func TestTokenFileReReadPerRequest(t *testing.T) {
	tokPath := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tokPath, []byte("tok-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, c := setup(t, tethermail.WithTokenFile(tokPath))
	s.SetToken("tok-one")
	if _, _, err := c.List(ctx(t), me.URN(), tethermail.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	s.SetToken("tok-two")
	_, _, err := c.List(ctx(t), me.URN(), tethermail.ListOptions{})
	if ae, ok := err.(*tethermail.APIError); !ok || ae.Status != 401 {
		t.Fatalf("want 401, got %v", err)
	}
	if err := os.WriteFile(tokPath, []byte("tok-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.List(ctx(t), me.URN(), tethermail.ListOptions{}); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	if err := os.Chmod(tokPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.List(ctx(t), me.URN(), tethermail.ListOptions{}); err == nil {
		t.Fatal("expected perms error")
	}
}
