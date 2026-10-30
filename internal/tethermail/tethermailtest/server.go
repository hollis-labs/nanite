// Package tethermailtest is an in-memory fake of Tether's mailbox routes.
package tethermailtest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/messaging"
)

type entry struct {
	seq int64
	env messaging.Envelope
}

type watcher struct {
	to   string
	ch   chan entry
	done chan struct{}
	once sync.Once
}

// Server is the fake. URL is the http address of the httptest server.
type Server struct {
	t   testing.TB
	URL string

	httpSrv *httptest.Server
	handler http.Handler

	mu       sync.Mutex
	token    string
	logs     map[string][]entry
	consumed map[string]bool
	sent     []messaging.Envelope
	idem     map[string]messaging.Envelope
	watchers map[*watcher]struct{}
	nextID   int
}

// NewServer starts the fake on an httptest server, closed at test cleanup.
func NewServer(t testing.TB) *Server {
	s := &Server{t: t, logs: map[string][]entry{}, consumed: map[string]bool{},
		idem: map[string]messaging.Envelope{}, watchers: map[*watcher]struct{}{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /messages/list", s.auth(s.list))
	mux.HandleFunc("GET /messages/watch", s.auth(s.watch))
	mux.HandleFunc("POST /messages/{id}/consume", s.auth(s.consume))
	mux.HandleFunc("POST /messages", s.auth(s.send))
	s.handler = mux
	s.httpSrv = httptest.NewServer(mux)
	s.URL = s.httpSrv.URL
	t.Cleanup(func() { s.DropWatchers(); s.httpSrv.Close() })
	return s
}

// ListenUnix additionally serves on a unix socket at path.
func (s *Server) ListenUnix(path string) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		s.t.Fatalf("tethermailtest: listen unix: %v", err)
	}
	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	s.t.Cleanup(func() { s.DropWatchers(); _ = srv.Close() })
}

// SetToken requires "Authorization: Bearer <token>" on every request.
func (s *Server) SetToken(tok string) { s.mu.Lock(); s.token = tok; s.mu.Unlock() }

// Add injects mail as a sender would and returns its per-recipient seq.
func (s *Server) Add(env messaging.Envelope) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addLocked(&env)
}

func (s *Server) addLocked(env *messaging.Envelope) int64 {
	if env.ID == "" {
		s.nextID++
		env.ID = fmt.Sprintf("msg-%d", s.nextID)
	}
	if env.CreatedAt.IsZero() {
		env.CreatedAt = time.Now().UTC()
	}
	to := env.To.URN()
	seq := int64(len(s.logs[to]) + 1)
	e := entry{seq: seq, env: *env}
	s.logs[to] = append(s.logs[to], e)
	for w := range s.watchers {
		if w.to == to {
			select {
			case w.ch <- e:
			default:
			}
		}
	}
	return seq
}

// Consumed reports whether the message was consumed.
func (s *Server) Consumed(id string) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.consumed[id] }

// Sent returns envelopes accepted via POST /messages (idempotent replays excluded).
func (s *Server) Sent() []messaging.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]messaging.Envelope(nil), s.sent...)
}

// DropWatchers closes all live watch streams.
func (s *Server) DropWatchers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for w := range s.watchers {
		w.once.Do(func() { close(w.done) })
	}
}

// WatcherCount reports live watch streams.
func (s *Server) WatcherCount() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.watchers) }

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		tok := s.token
		s.mu.Unlock()
		if tok != "" && r.Header.Get("Authorization") != "Bearer "+tok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) match(e entry, r *http.Request) bool {
	q := r.URL.Query()
	if k := q.Get("kind"); k != "" && string(e.env.Kind) != k {
		return false
	}
	if t := q.Get("thread_id"); t != "" && e.env.ThreadID != t {
		return false
	}
	if q.Get("unread_only") == "true" && s.consumed[e.env.ID] {
		return false
	}
	return true
}

func (s *Server) mailbox(w http.ResponseWriter, r *http.Request) (string, bool) {
	to, as := r.URL.Query().Get("to"), r.URL.Query().Get("as")
	if to == "" || to != as {
		http.Error(w, "to and as must be equal", http.StatusBadRequest)
		return "", false
	}
	return to, true
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	to, ok := s.mailbox(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	s.mu.Lock()
	var all []messaging.Envelope
	for _, e := range s.logs[to] {
		if s.match(e, r) {
			env := e.env
			if s.consumed[env.ID] {
				now := time.Now().UTC()
				env.ConsumedAt = &now
			}
			all = append(all, env)
		}
	}
	s.mu.Unlock()
	total := len(all)
	if offset > total {
		offset = total
	}
	all = all[offset:]
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	if all == nil {
		all = []messaging.Envelope{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"messages": all, "total": total, "limit": limit})
}

func (s *Server) watch(w http.ResponseWriter, r *http.Request) {
	to, ok := s.mailbox(w, r)
	if !ok {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flush", http.StatusInternalServerError)
		return
	}
	afterStr := r.URL.Query().Get("after_seq")
	if afterStr == "" {
		afterStr = r.Header.Get("Last-Event-ID")
	}
	s.mu.Lock()
	head := int64(len(s.logs[to]))
	var after int64
	if afterStr == "" {
		after = head
	} else {
		after, _ = strconv.ParseInt(afterStr, 10, 64)
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	if after > head {
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: gap\ndata: {\"reason\":\"ahead\"}\n\n")
		fl.Flush()
		return
	}
	var replay []entry
	for _, e := range s.logs[to] {
		if e.seq > after && s.match(e, r) {
			replay = append(replay, e)
		}
	}
	wt := &watcher{to: to, ch: make(chan entry, 256), done: make(chan struct{})}
	s.watchers[wt] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.watchers, wt); s.mu.Unlock() }()

	w.WriteHeader(http.StatusOK)
	fl.Flush()
	write := func(e entry) {
		b, _ := json.Marshal(e.env)
		// splice seq into the envelope projection
		m := map[string]json.RawMessage{}
		_ = json.Unmarshal(b, &m)
		m["seq"] = json.RawMessage(strconv.FormatInt(e.seq, 10))
		b, _ = json.Marshal(m)
		fmt.Fprintf(w, "id: %d\nevent: message\ndata: %s\n\n", e.seq, b)
	}
	for _, e := range replay {
		write(e)
	}
	fmt.Fprint(w, "event: synchronized\ndata: {}\n\n")
	fl.Flush()
	last := head
	for _, e := range replay {
		last = max(last, e.seq)
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-wt.done:
			return
		case e := <-wt.ch:
			if e.seq <= last || !func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.match(e, r) }() {
				continue
			}
			last = e.seq
			write(e)
			fl.Flush()
		case <-time.After(15 * time.Second):
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) consume(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("as") == "" {
		http.Error(w, "as required", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	found := false
	for _, l := range s.logs {
		for _, e := range l {
			if e.env.ID == id {
				found = true
			}
		}
	}
	if found {
		s.consumed[id] = true
	}
	s.mu.Unlock()
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var env messaging.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	idk := env.From.URN() + "|" + key
	s.mu.Lock()
	if key != "" {
		if prev, ok := s.idem[idk]; ok {
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, prev)
			return
		}
	}
	env.ID = ""
	s.addLocked(&env)
	s.sent = append(s.sent, env)
	if key != "" {
		s.idem[idk] = env
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, env)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
