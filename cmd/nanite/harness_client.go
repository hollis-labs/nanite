package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// harnessClient is a thin HTTP client for the /api/harness/v1 control-plane
// API (internal/api/harness_v1.go) — the same GUI-agnostic surface the React
// UI is thin over. It talks to an already-running `nanite serve` process; it
// never opens the database directly (internal/store is single-writer, and a
// second OS process touching it concurrently would risk contention with the
// running service). Wire types here are local mirrors of harness_v1's JSON
// shapes since those request/response structs are unexported in internal/api.
type harnessClient struct {
	baseURL string
	http    *http.Client
}

// jsonCallTimeout bounds the control-plane JSON calls (create/get session,
// send turn, cancel) only. It must never apply to StreamEvents: http.Client's
// Timeout covers the full response including body reads, and an SSE turn
// stream commonly runs far longer than any single control-plane call — a
// shared timeout would truncate legitimate long-running turns mid-stream.
const jsonCallTimeout = 30 * time.Second

func newHarnessClient(baseURL string) *harnessClient {
	if baseURL == "" {
		baseURL = apiBaseURL()
	}
	return &harnessClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{},
	}
}

func (c *harnessClient) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
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
	if user := os.Getenv(brand.Env("AUTH_USER")); user != "" {
		req.SetBasicAuth(user, os.Getenv(brand.Env("AUTH_PASSWORD")))
	}
	return req, nil
}

func (c *harnessClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, jsonCallTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w (is `nanite serve` running?)", c.baseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

type harnessCreateSessionRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	Title       string `json:"title,omitempty"`
}

type harnessSessionResponse struct {
	Session *store.Session `json:"session"`
}

type harnessTurnRequest struct {
	Content string `json:"content"`
}

type harnessTurnResponse struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	StreamURL string `json:"stream_url"`
}

type harnessCancelResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

func (c *harnessClient) CreateSession(ctx context.Context, req harnessCreateSessionRequest) (*store.Session, error) {
	var resp harnessSessionResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/harness/v1/sessions", req, &resp); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	if resp.Session == nil {
		return nil, fmt.Errorf("create session: empty session in response")
	}
	return resp.Session, nil
}

func (c *harnessClient) GetSession(ctx context.Context, sessionID string) (*store.Session, error) {
	var resp harnessSessionResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/harness/v1/sessions/"+sessionID, nil, &resp); err != nil {
		return nil, fmt.Errorf("get session %s: %w", sessionID, err)
	}
	if resp.Session == nil {
		return nil, fmt.Errorf("get session %s: empty session in response", sessionID)
	}
	return resp.Session, nil
}

func (c *harnessClient) SendTurn(ctx context.Context, sessionID, content string) (*harnessTurnResponse, error) {
	var resp harnessTurnResponse
	req := harnessTurnRequest{Content: content}
	if err := c.doJSON(ctx, http.MethodPost, "/api/harness/v1/sessions/"+sessionID+"/turns", req, &resp); err != nil {
		return nil, fmt.Errorf("send turn: %w", err)
	}
	return &resp, nil
}

func (c *harnessClient) Cancel(ctx context.Context, sessionID string) (*harnessCancelResponse, error) {
	var resp harnessCancelResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/harness/v1/sessions/"+sessionID+"/cancel", nil, &resp); err != nil {
		return nil, fmt.Errorf("cancel: %w", err)
	}
	return &resp, nil
}

// StreamEvents opens the SSE stream at path — the harness-v1 turn
// response's stream_url (e.g. "/api/harness/v1/sessions/{id}/events?
// message_id={id}") — and parses events onto the returned channel, closing
// it when the stream ends (a stream_end event, server EOF/error, or ctx
// cancellation). The caller must drain the channel to avoid leaking the
// underlying goroutine/response body.
//
// Takes the server-provided path rather than reconstructing it from
// sessionID/messageID: harness-v1 owns its own route shape, and a client
// that re-derives the URL duplicates that routing knowledge and breaks
// silently if the server ever changes it.
func (c *harnessClient) StreamEvents(ctx context.Context, path string) (<-chan chat.StreamEvent, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w (is `nanite serve` running?)", c.baseURL, err)
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	out := make(chan chat.StreamEvent, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var dataLines []string
		flush := func() bool {
			if len(dataLines) == 0 {
				return true
			}
			payload := strings.Join(dataLines, "\n")
			dataLines = nil
			var evt chat.StreamEvent
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				return true
			}
			select {
			case out <- evt:
			case <-ctx.Done():
				return false
			}
			return evt.Type != "stream_end"
		}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if !flush() {
					return
				}
				continue
			}
			if rest, ok := strings.CutPrefix(line, "data:"); ok {
				dataLines = append(dataLines, strings.TrimPrefix(rest, " "))
			}
			// event:/id:/comment lines carry no information this client
			// needs beyond what's already in the data: JSON payload's own
			// "type" field.
			if ctx.Err() != nil {
				return
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			// A network read failure or bufio.ErrTooLong (a line exceeded
			// the scanner's max buffer) otherwise terminates this goroutine
			// silently — the channel just closes with no hint why. Surface
			// it as a synthetic error event so the caller can diagnose it
			// instead of seeing an unexplained stream end.
			select {
			case out <- chat.StreamEvent{Type: "error", Error: fmt.Sprintf("event stream read failed: %v", scanErr)}:
			case <-ctx.Done():
			}
			return
		}
		flush()
	}()
	return out, nil
}
