// Package tethermail is a small typed HTTP client for Tether's non-destructive
// mailbox routes: list, watch (SSE with replay), consume and send.
package tethermail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/messaging"
)

const (
	requestTimeout = 10 * time.Second
	maxTokenLen    = 4096
)

// Client talks to Tether's mailbox routes.
type Client struct {
	baseURL   string
	http      *http.Client
	tokenFile string
}

// Option configures a Client.
type Option func(*Client)

// WithTokenFile reads the bearer token from path (mode 0600) on every request,
// so token rotation takes effect without a restart. The token is never logged.
func WithTokenFile(path string) Option { return func(c *Client) { c.tokenFile = path } }

// WithHTTPClient overrides the HTTP client (tests). The base URL still comes
// from addr. The client must not set a Timeout that would cut SSE streams.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// APIError is a non-success HTTP response.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tethermail: http %d: %s", e.Status, e.Body)
}

// New builds a client. addr is a unix socket path, unix:///path (or unix:/path),
// tcp:host:port, or an http(s):// URL.
func New(addr string, opts ...Option) (*Client, error) {
	if addr == "" {
		return nil, errors.New("tethermail: address required")
	}
	if strings.HasPrefix(addr, "unix:~/") || strings.HasPrefix(addr, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			addr = strings.Replace(addr, "~/", home+"/", 1)
		}
	}
	c := &Client{}
	switch {
	case strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://"):
		c.baseURL = strings.TrimRight(addr, "/")
		c.http = &http.Client{}
	case strings.HasPrefix(addr, "tcp:"):
		hp := strings.TrimPrefix(addr, "tcp:")
		if hp == "" {
			return nil, errors.New("tethermail: tcp address missing host:port")
		}
		c.baseURL = "http://" + hp
		c.http = &http.Client{}
	default:
		path := addr
		if strings.HasPrefix(path, "unix://") {
			path = strings.TrimPrefix(path, "unix://")
		} else if strings.HasPrefix(path, "unix:") {
			path = strings.TrimPrefix(path, "unix:")
		}
		if path == "" || !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("tethermail: unsupported address %q", addr)
		}
		path = filepath.Clean(path)
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
		c.baseURL = "http://unix"
		c.http = &http.Client{Transport: tr}
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (c *Client) token() (string, error) {
	if c.tokenFile == "" {
		return "", nil
	}
	info, err := os.Stat(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("tethermail: token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("tethermail: token file must be a regular file with mode 0600")
	}
	body, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("tethermail: token file: %w", err)
	}
	tok := strings.TrimSpace(string(body))
	if tok == "" || len(tok) > maxTokenLen {
		return "", errors.New("tethermail: invalid token file content")
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < 33 || tok[i] > 126 {
			return "", errors.New("tethermail: invalid token file content")
		}
	}
	return tok, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Request, error) {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	tok, err := c.token()
	if err != nil {
		return nil, err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func apiError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
}

// do runs a request with a timeout and returns the response with the body
// still open; the caller closes it.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body []byte, hdr map[string]string) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := c.newRequest(ctx, method, path, q, r)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return resp, cancel, nil
}

// ListOptions narrows List.
type ListOptions struct {
	Kind, ThreadID  string
	UnreadOnly      bool
	IncludeArchived bool
	Limit, Offset   int
}

// List reads the mailbox `to` without marking anything delivered or read.
func (c *Client) List(ctx context.Context, to string, o ListOptions) ([]messaging.Envelope, int, error) {
	q := url.Values{}
	q.Set("to", to)
	q.Set("as", to)
	if o.Kind != "" {
		q.Set("kind", o.Kind)
	}
	if o.ThreadID != "" {
		q.Set("thread_id", o.ThreadID)
	}
	if o.UnreadOnly {
		q.Set("unread_only", "true")
	}
	if o.IncludeArchived {
		q.Set("include_archived", "true")
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Offset > 0 {
		q.Set("offset", strconv.Itoa(o.Offset))
	}
	resp, cancel, err := c.do(ctx, http.MethodGet, "/messages/list", q, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, apiError(resp)
	}
	var out struct {
		Messages []messaging.Envelope `json:"messages"`
		Total    int                  `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("tethermail: decode list: %w", err)
	}
	return out.Messages, out.Total, nil
}

// Consume records a handled-ack for (messageID, as).
func (c *Client) Consume(ctx context.Context, messageID, as string) error {
	q := url.Values{}
	q.Set("as", as)
	resp, cancel, err := c.do(ctx, http.MethodPost, "/messages/"+url.PathEscape(messageID)+"/consume", q, nil, nil)
	if err != nil {
		return err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	return nil
}

// MarkRead marks a message read for recipient `as` (presentation only: it keeps
// handled mail out of unread_only lists; it is not an acknowledgement of work).
func (c *Client) MarkRead(ctx context.Context, messageID, as string) error {
	q := url.Values{}
	q.Set("as", as)
	resp, cancel, err := c.do(ctx, http.MethodPost, "/messages/"+url.PathEscape(messageID)+"/read", q, nil, nil)
	if err != nil {
		return err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	return nil
}

// Send posts an envelope. A non-empty idempotencyKey is sent as the
// Idempotency-Key header; the same key from the same sender returns the
// earlier message.
func (c *Client) Send(ctx context.Context, env messaging.Envelope, idempotencyKey string) (messaging.Envelope, error) {
	env.ID = ""
	env.CreatedAt = time.Time{}
	body, err := json.Marshal(env)
	if err != nil {
		return messaging.Envelope{}, err
	}
	var hdr map[string]string
	if idempotencyKey != "" {
		hdr = map[string]string{"Idempotency-Key": idempotencyKey}
	}
	resp, cancel, err := c.do(ctx, http.MethodPost, "/messages", nil, body, hdr)
	if err != nil {
		return messaging.Envelope{}, err
	}
	defer cancel()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return messaging.Envelope{}, apiError(resp)
	}
	var out messaging.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return messaging.Envelope{}, fmt.Errorf("tethermail: decode send: %w", err)
	}
	return out, nil
}

// EventKind is an SSE event type from /messages/watch.
type EventKind string

const (
	EventMessage      EventKind = "message"
	EventSynchronized EventKind = "synchronized"
	EventGap          EventKind = "gap"
)

// Event is one watch event.
type Event struct {
	Kind   EventKind
	Seq    int64
	Env    messaging.Envelope
	Reason string // gap reason ("ahead", "restart")
}

// WatchOptions narrows Watch.
type WatchOptions struct {
	Kind, ThreadID string
	UnreadOnly     bool
}

// Watch opens the side-effect-free SSE stream for mailbox `to`. afterSeq == 0
// means "from now" (after_seq omitted); afterSeq > 0 replays newer messages
// first. The channel closes when ctx ends, the stream drops, or after a gap
// event (delivered first). Open errors are returned directly.
func (c *Client) Watch(ctx context.Context, to string, afterSeq int64, o WatchOptions) (<-chan Event, error) {
	q := url.Values{}
	q.Set("to", to)
	q.Set("as", to)
	if afterSeq > 0 {
		q.Set("after_seq", strconv.FormatInt(afterSeq, 10))
	}
	if o.Kind != "" {
		q.Set("kind", o.Kind)
	}
	if o.ThreadID != "" {
		q.Set("thread_id", o.ThreadID)
	}
	if o.UnreadOnly {
		q.Set("unread_only", "true")
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/messages/watch", q, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, apiError(resp)
	}
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()
		parseSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}

// parseSSE reads SSE frames from r and emits events until EOF, error, ctx end
// or a gap event.
func parseSSE(ctx context.Context, r io.Reader, out chan<- Event) {
	br := bufio.NewReaderSize(r, 64*1024)
	var id, name string
	var data []string
	reset := func() { id, name, data = "", "", nil }
	for {
		line, err := readLine(br)
		if err != nil {
			return
		}
		if line == "" { // dispatch
			if name != "" || len(data) > 0 {
				ev, ok := buildEvent(id, name, strings.Join(data, "\n"))
				if ok {
					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}
					if ev.Kind == EventGap {
						return
					}
				}
			}
			reset()
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			id = value
		case "event":
			name = value
		case "data":
			data = append(data, value)
		}
	}
}

// readLine reads one line of any length, stripping the line terminator.
func readLine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			return "", err
		}
		sb.Write(chunk)
		if !isPrefix {
			return sb.String(), nil
		}
	}
}

func buildEvent(id, name, data string) (Event, bool) {
	if name == "" {
		name = string(EventMessage) // SSE default event type
	}
	switch EventKind(name) {
	case EventMessage:
		var env messaging.Envelope
		if err := json.Unmarshal([]byte(data), &env); err != nil {
			return Event{}, false
		}
		var seq int64
		if id != "" {
			seq, _ = strconv.ParseInt(id, 10, 64)
		}
		if seq == 0 {
			var s struct {
				Seq int64 `json:"seq"`
			}
			_ = json.Unmarshal([]byte(data), &s)
			seq = s.Seq
		}
		return Event{Kind: EventMessage, Seq: seq, Env: env}, true
	case EventSynchronized:
		return Event{Kind: EventSynchronized}, true
	case EventGap:
		var g struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal([]byte(data), &g)
		return Event{Kind: EventGap, Reason: g.Reason}, true
	}
	return Event{}, false // unknown events ignored
}
