package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"net/url"

	chatstream "github.com/hollis-labs/go-chatstream"
	"github.com/hollis-labs/go-chatstream/framing"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// Bounds for agentClient's connection-establishment retry. CW-20260813-0008.
const (
	agentConnectMaxAttempts  = 4 // 1 initial try + 3 retries — bounded, never infinite
	agentConnectInitialDelay = 250 * time.Millisecond
	agentConnectMaxDelay     = 2 * time.Second
)

// connectError marks a failure to reach the agent-v1 server at all — the
// request never produced an HTTP response (dial failure, timeout, connection
// reset by a server restarting mid-session) — as distinct from a failure
// after the server answered (a non-2xx status, an unparsable body). Only a
// *connectError is eligible for retryConnect: a request that got a real
// answer back must not be blindly retried, since the server may already
// have acted on it.
type connectError struct {
	err error
}

type agentHTTPError struct {
	status  int
	message string
}

func (e *agentHTTPError) Error() string {
	return fmt.Sprintf("server returned %d: %s", e.status, e.message)
}

func (e *connectError) Error() string { return e.err.Error() }
func (e *connectError) Unwrap() error { return e.err }

// retryConnect calls attempt up to agentConnectMaxAttempts times, retrying
// only on a *connectError, with bounded exponential backoff between
// attempts. Any other error — including a definitive non-2xx response —
// returns immediately without retrying.
//
// Scope is deliberately narrow to the read-only stream connection.
// Effectful turn submissions never use this retry loop. Once
// StreamEvents has returned its event channel, a turn is considered to be
// actively streaming — a failure from that point on (a read error, a
// malformed frame) must surface as a clear, non-retried error instead,
// since retrying after partial output has reached the user risks duplicate
// or re-run output. See StreamEvents' decode loop and runChatTurn in
// chat_cmd.go, which advises the user to resume via `--session` instead.
//
// Shares its backoff arithmetic with the auto-start health poll
// (CW-20260813-0007, pollHealthUntilReady) via nextBackoffDelay rather than
// duplicating a second retry loop.
func retryConnect[T any](ctx context.Context, attempt func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	var delay time.Duration
	for i := 0; i < agentConnectMaxAttempts; i++ {
		if i > 0 {
			delay = nextBackoffDelay(delay, agentConnectInitialDelay, agentConnectMaxDelay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return zero, ctx.Err()
			case <-timer.C:
			}
		}
		result, err := attempt()
		if err == nil {
			return result, nil
		}
		var connErr *connectError
		if !errors.As(err, &connErr) {
			return zero, err
		}
		lastErr = err
	}
	return zero, lastErr
}

// agentClient is a thin HTTP client for the /api/agent/v1 control-plane
// API (internal/api/agent_v1.go) — the same GUI-agnostic surface the React
// UI is thin over. It talks to an already-running `nanite serve` process; it
// never opens the database directly (internal/store is single-writer, and a
// second OS process touching it concurrently would risk contention with the
// running service). Wire types here are local mirrors of agent_v1's JSON
// shapes since those request/response structs are unexported in internal/api.
type agentClient struct {
	baseURL string
	http    *http.Client
}

// jsonCallTimeout bounds the control-plane JSON calls (create/get session,
// send turn, cancel) only. It must never apply to StreamEvents: http.Client's
// Timeout covers the full response including body reads, and an SSE turn
// stream commonly runs far longer than any single control-plane call — a
// shared timeout would truncate legitimate long-running turns mid-stream.
const jsonCallTimeout = 30 * time.Second

func newAgentClient(baseURL string) *agentClient {
	if baseURL == "" {
		baseURL = apiBaseURL()
	}
	return &agentClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (c *agentClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Same NANITE_AUTH_USER/NANITE_AUTH_PASSWORD convention as apiPost in
	// plugin_dev_cmd.go; no-op when unset since basicAuthMiddleware is also
	// a no-op then (local dev default).
	if token := os.Getenv(brand.Env("AUTH_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if user := os.Getenv(brand.Env("AUTH_USER")); user != "" {
		req.SetBasicAuth(user, os.Getenv(brand.Env("AUTH_PASSWORD")))
	}
	return req, nil
}

func (c *agentClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, jsonCallTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &connectError{fmt.Errorf("could not reach %s: %w (is `nanite serve` running?)", c.baseURL, err)}
	}
	defer func() {
		_ = resp.Body.Close() // Response-body close is best-effort cleanup after the request result is read.
	}()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &agentHTTPError{status: resp.StatusCode, message: strings.TrimSpace(string(data))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

type agentCreateSessionRequest struct {
	DefinitionRef service.DefinitionRef `json:"definition_ref"`
	ProjectID     string                `json:"project_id,omitempty"`
	Title         string                `json:"title,omitempty"`
}

type agentSessionResponse struct {
	Session *store.Session `json:"session"`
}

type agentTextInput struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type agentTurnRequest struct {
	Content  []agentTextInput `json:"content"`
	Delivery string           `json:"delivery"`
}

type agentTurnResponse struct {
	SessionID string `json:"session_view_id"`
	MessageID string `json:"output_message_id"`
	TurnID    string `json:"turn_id"`
	StreamURL string `json:"stream_url"`
}

type agentCancelResponse struct {
	SessionID string `json:"session_view_id"`
	Status    string `json:"state"`
}

func (c *agentClient) CreateSession(ctx context.Context, req agentCreateSessionRequest) (*store.Session, error) {
	if req.DefinitionRef.DefinitionID == "" {
		var init struct {
			DefaultDefinitionRef service.DefinitionRef `json:"default_definition_ref"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/api/agent/v1/initialize", nil, &init); err != nil {
			return nil, fmt.Errorf("initialize: %w", err)
		}
		req.DefinitionRef = init.DefaultDefinitionRef
	}
	if err := req.DefinitionRef.Validate(); err != nil {
		return nil, err
	}
	var resp agentSessionResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/agent/v1/sessions", req, &resp); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	if resp.Session == nil {
		return nil, fmt.Errorf("create session: empty session in response")
	}
	return resp.Session, nil
}

func (c *agentClient) GetSession(ctx context.Context, sessionID string) (*store.Session, error) {
	var resp agentSessionResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/agent/v1/sessions/"+url.PathEscape(sessionID), nil, &resp); err != nil {
		return nil, fmt.Errorf("get session %s: %w", sessionID, err)
	}
	if resp.Session == nil {
		return nil, fmt.Errorf("get session %s: empty session in response", sessionID)
	}
	return resp.Session, nil
}

func (c *agentClient) SendTurn(ctx context.Context, sessionID, content string) (*agentTurnResponse, error) {
	req := agentTurnRequest{Content: []agentTextInput{{Kind: "text", Text: content}}, Delivery: "at_idle"}
	var resp agentTurnResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/agent/v1/sessions/"+url.PathEscape(sessionID)+"/turns", req, &resp)
	if err != nil {
		var rejected *agentHTTPError
		if errors.As(err, &rejected) && rejected.status >= 400 && rejected.status < 500 {
			return nil, fmt.Errorf("send turn rejected: %w", err)
		}
		return nil, fmt.Errorf("send turn outcome may be uncertain; inspect the session before submitting again: %w", err)
	}
	return &resp, nil
}

func (c *agentClient) Cancel(ctx context.Context, sessionID, turnID string) (*agentCancelResponse, error) {
	var resp agentCancelResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/agent/v1/sessions/"+url.PathEscape(sessionID)+"/turns/"+url.PathEscape(turnID)+"/cancel", nil, &resp); err != nil {
		return nil, fmt.Errorf("cancel: %w", err)
	}
	return &resp, nil
}

// StreamEvents decodes only the advertised canonical wire using shared SSE
// framing and reduction. A transport EOF is not a committed turn outcome.
// The caller can load status/snapshot and explicitly resume the same turn.
func (c *agentClient) StreamEvents(ctx context.Context, path, runID string) (<-chan chat.StreamEvent, error) {
	if !strings.HasPrefix(path, "/api/agent/v1/sessions/") || !strings.HasSuffix(path, "/events") || strings.ContainsAny(path, "?#") {
		return nil, errors.New("invalid native turn event path")
	}
	segments := strings.Split(path, "/")
	if len(segments) != 9 || segments[6] != "turns" || segments[5] == "" || runID == "" {
		return nil, errors.New("invalid native turn event path")
	}
	pathRunID, pathErr := url.PathUnescape(segments[7])
	if pathErr != nil || pathRunID != runID {
		return nil, errors.New("native event path does not match the accepted turn")
	}
	resp, err := retryConnect(ctx, func() (*http.Response, error) {
		req, err := c.newRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "text/event-stream")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, &connectError{err}
		}
		return resp, nil
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		return nil, &agentHTTPError{resp.StatusCode, strings.TrimSpace(string(data))}
	}
	out := make(chan chat.StreamEvent, 16)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		emit := func(e chat.StreamEvent) bool {
			select {
			case out <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var message *chatstream.Message
		var checkpoint uint64
		for frame, err := range framing.SSE(resp.Body) {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				emit(chat.StreamEvent{Type: "error", Error: "native event stream read failed: " + err.Error()})
				return
			}
			var event chatstream.Event
			if json.Unmarshal(frame.Data, &event) != nil || event.V != chatstream.SchemaVersion || frame.Event != string(event.Verb) {
				emit(chat.StreamEvent{Type: "error", Error: "malformed canonical event received"})
				return
			}
			if event.Verb == chatstream.VerbGap {
				emit(chat.StreamEvent{Type: "error", Error: "event log gap; load the turn status/snapshot before resuming"})
				return
			}
			if frame.ID != fmt.Sprint(event.Seq) || event.Seq == 0 {
				emit(chat.StreamEvent{Type: "error", Error: "invalid canonical event checkpoint"})
				return
			}
			if event.RunID != runID || event.Seq != checkpoint+1 || (checkpoint == 0 && event.Verb != chatstream.VerbRunStart) {
				emit(chat.StreamEvent{Type: "error", Error: "canonical event does not match the requested run or next checkpoint"})
				return
			}
			message, err = chatstream.Reduce([]chatstream.Event{event}, message)
			if err != nil {
				emit(chat.StreamEvent{Type: "error", Error: "invalid canonical event sequence: " + err.Error()})
				return
			}
			checkpoint = event.Seq
			display := chat.StreamEvent{}
			switch event.Verb {
			case chatstream.VerbPartDelta:
				phase := ""
				for _, part := range message.Parts {
					if part.ID == event.PartID {
						_ = json.Unmarshal(part.Meta[chatstream.MetaPhase], &phase)
						if part.Kind == chatstream.PartText {
							display = chat.StreamEvent{Type: "delta", Content: event.Text, Phase: phase}
						}
					}
				}
			case chatstream.VerbPartStart:
				if event.Kind == string(chatstream.PartToolCall) {
					display.Type = "tool_call"
					_ = json.Unmarshal(event.Meta[chatstream.MetaName], &display.Tool)
					_ = json.Unmarshal(event.Meta[chatstream.MetaDetail], &display.Detail)
				}
			case chatstream.VerbPartEnd:
				for _, part := range message.Parts {
					if part.ID == event.PartID && part.Kind == chatstream.PartToolResult {
						display.Type = "tool_result"
						_ = json.Unmarshal(part.Meta[chatstream.MetaIsError], &display.IsError)
						var value struct {
							Summary string `json:"summary"`
						}
						_ = json.Unmarshal(part.Final, &value)
						display.Summary = value.Summary
					}
				}
			case chatstream.VerbApprovalRequest:
				var descriptor struct {
					Tool  string         `json:"tool"`
					Input map[string]any `json:"input"`
				}
				_ = json.Unmarshal(event.Descriptor, &descriptor)
				data, _ := json.Marshal(chat.ApprovalRequestPayload{RequestID: event.ApprovalID, RunID: event.RunID, CallID: event.CallID, Tool: descriptor.Tool, Input: descriptor.Input, Reason: event.Reason, SupportedScopes: []string{"once"}})
				display = chat.StreamEvent{Type: "approval_request", Data: string(data)}
			case chatstream.VerbActivity:
				if event.Kind == chatstream.ActivityReplaceContent {
					var value struct {
						Content string `json:"content"`
					}
					_ = json.Unmarshal(event.Value, &value)
					display = chat.StreamEvent{Type: "replace_content", Content: value.Content}
				}
			case chatstream.VerbRunError:
				display = chat.StreamEvent{Type: "error", Error: event.Message}
			case chatstream.VerbRunAbort:
				display = chat.StreamEvent{Type: "error", Error: "turn canceled"}
			case chatstream.VerbRunStart, chatstream.VerbStepStart, chatstream.VerbStepFinish, chatstream.VerbMessageStart, chatstream.VerbMessageEnd, chatstream.VerbUsage, chatstream.VerbRaw, chatstream.VerbGap:
				// These canonical events update reduction without an inline marker.
			case chatstream.VerbRunFinish:
				display = chat.StreamEvent{Type: "stream_end"}
			}
			if display.Type != "" && !emit(display) {
				return
			}
			if event.IsTerminal() {
				return
			}
		}
		if ctx.Err() == nil {
			emit(chat.StreamEvent{Type: "error", Error: "event connection ended without a terminal outcome; inspect the turn status/snapshot"})
		}
	}()
	return out, nil
}
