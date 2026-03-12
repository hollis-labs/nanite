// Package cli provides a terminal client for the mentat-chat HTTP API.
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client communicates with the mentat-chat HTTP API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates a client pointing at the given base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// HealthResponse is the shape of GET /api/health.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Health checks whether the server is up.
func (c *Client) Health() (*HealthResponse, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	var h HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Session represents a chat session.
type Session struct {
	ID          string `json:"id"`
	ShortCode   string `json:"short_code"`
	Title       string `json:"title"`
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Status      string `json:"status"`
}

// CreateSessionRequest is the body for POST /api/sessions.
type CreateSessionRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id,omitempty"`
	Model       string `json:"model,omitempty"`
	Provider    string `json:"provider,omitempty"`
}

// CreateSession creates a new chat session.
func (c *Client) CreateSession(req CreateSessionRequest) (*Session, error) {
	body, _ := json.Marshal(req)
	resp, err := c.HTTPClient.Post(c.BaseURL+"/api/sessions", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create session returned %d: %s", resp.StatusCode, string(b))
	}
	var s Session
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SendMessageResponse is the body of POST /api/messages.
type SendMessageResponse struct {
	MessageID string `json:"message_id"`
	StreamURL string `json:"stream_url"`
}

// SendMessage sends a user message and returns the stream URL.
func (c *Client) SendMessage(sessionID, content string) (*SendMessageResponse, error) {
	payload, _ := json.Marshal(map[string]string{
		"session_id": sessionID,
		"content":    content,
	})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/api/messages", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("send message returned %d: %s", resp.StatusCode, string(b))
	}
	var r SendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// StreamEvent represents a single SSE event from the response stream.
type StreamEvent struct {
	Type      string          `json:"type"`
	Content   string          `json:"content,omitempty"`
	MessageID string          `json:"message_id,omitempty"`
	AgentID   string          `json:"agent_id,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	ToolID    string          `json:"tool_id,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Error     string          `json:"error,omitempty"`
	Usage     *StreamUsage    `json:"usage,omitempty"`
}

// StreamUsage holds token usage stats from stream_end events.
type StreamUsage struct {
	InputTokens         int    `json:"input_tokens"`
	OutputTokens        int    `json:"output_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	CacheReadTokens     int    `json:"cache_read_tokens"`
	StopReason          string `json:"stop_reason"`
}

// StreamResponse connects to the SSE stream and calls handler for each event.
// The handler returns false to stop reading.
func (c *Client) StreamResponse(streamURL string, handler func(StreamEvent) bool) error {
	url := streamURL
	if !strings.HasPrefix(url, "http") {
		url = c.BaseURL + streamURL
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	// Use a client without timeout for streaming.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stream returned %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var evt StreamEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				// If JSON parse fails, treat as plain text delta.
				evt = StreamEvent{Type: eventType, Content: data}
			}
			if evt.Type == "" {
				evt.Type = eventType
			}
			if !handler(evt) {
				return nil
			}
		}
	}
	return scanner.Err()
}

// Workspace represents a workspace.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListWorkspaces returns all workspaces.
func (c *Client) ListWorkspaces() ([]Workspace, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/workspaces")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list workspaces returned %d", resp.StatusCode)
	}
	var ws []Workspace
	if err := json.NewDecoder(resp.Body).Decode(&ws); err != nil {
		return nil, err
	}
	return ws, nil
}
