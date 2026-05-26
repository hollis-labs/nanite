// Package tether adapts the Tether daemon's AI proxy (/ai/chat[/stream]) to the
// nanite llmcontracts.Provider interface, so Tether is selectable as an API
// provider alongside Anthropic/OpenAI. The Tether daemon routes the request to
// whichever upstream model it's configured for; nanite just speaks its proxy
// protocol via github.com/hollis-labs/go-tether-client.
package tether

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
	tetherclient "github.com/hollis-labs/go-tether-client"
)

// callerID identifies nanite to the Tether AI audit log.
const callerID = "nanite"

// Client implements llmcontracts.Provider over the Tether AI proxy.
type Client struct {
	tc *tetherclient.Client
}

// New builds a Tether provider that talks to the daemon at the given unix
// socket path. Uses a no-timeout HTTP client (the AI proxy streams turns that
// outlast any fixed transport timeout; the caller's context bounds the call).
func New(socketPath string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}
	httpClient := &http.Client{Transport: transport} // Timeout: 0 — context-bounded.
	tc := tetherclient.MustNew("unix:"+socketPath, tetherclient.WithHTTPClient(httpClient))
	return &Client{tc: tc}
}

// DefaultSocketPath returns the conventional Tether daemon socket path
// (~/.tether/run/muxd.sock), or "" if the home dir can't be resolved.
func DefaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".tether", "run", "muxd.sock")
}

// SocketAvailable reports whether a Tether daemon socket exists at path — used
// to gate provider registration so "tether" only appears when the daemon is up.
func SocketAvailable(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

// StreamChat streams a response from the Tether AI proxy, mapping its
// AIStreamEvent frames onto llmtypes.StreamEvent.
func (c *Client) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	evCh, errCh, err := c.tc.AIChatStream(ctx, buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	out := make(chan llmtypes.StreamEvent, 16)
	go func() {
		defer close(out)
		var usage *llmtypes.Usage
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-evCh:
				if !ok {
					if usage != nil {
						emit(ctx, out, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: usage})
					}
					emit(ctx, out, llmtypes.StreamEvent{Type: llmtypes.EventDone})
					return
				}
				if ev.Error != "" {
					emit(ctx, out, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: ev.Error})
					return
				}
				if ev.Delta != "" {
					emit(ctx, out, llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: ev.Delta})
				}
				if u := mapUsage(ev.Usage); u != nil {
					usage = u
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil // closed — stop selecting it; keep draining evCh.
					continue
				}
				if e != nil {
					emit(ctx, out, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: e.Error()})
					return
				}
			}
		}
	}()
	return out, nil
}

// Complete makes a non-streaming completion call against the Tether AI proxy.
func (c *Client) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	resp, err := c.tc.AIChat(ctx, buildRequest(req, false))
	if err != nil {
		return "", err
	}
	return extractText(resp.Response.Output), nil
}

// Capabilities reports a conservative capability set. Tool calling is not yet
// mapped through the proxy adapter (deferred); streaming + system prompt work.
func (c *Client) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{
		SupportsStreamJSON: true,
		MaxTokens:          8192,
		ContextWindowSize:  200000,
	}
}

// buildRequest maps an llmtypes.ChatRequest onto the Tether AI request shape.
// The system prompt becomes a leading system message; each chat message becomes
// a single text content part. Tools/attachments are not mapped in this pass.
func buildRequest(req llmtypes.ChatRequest, streaming bool) tetherclient.ChatRequest {
	var input []tetherclient.AIMessage
	if strings.TrimSpace(req.SystemPrompt) != "" {
		input = append(input, textMessage("system", req.SystemPrompt))
	}
	for _, m := range req.Messages {
		input = append(input, textMessage(m.Role, m.Content))
	}
	// "auto" (the seeded default model) means "let Tether route by its own
	// policy" — send no model hint. A concrete model passes through as a hint.
	modelHint := req.Model
	if modelHint == "auto" {
		modelHint = ""
	}
	return tetherclient.ChatRequest{Request: tetherclient.AIRequest{
		Operation:       "chat",
		ModelHint:       modelHint,
		Streaming:       streaming,
		MaxOutputTokens: req.MaxTokens,
		CallerID:        callerID,
		Input:           input,
	}}
}

func textMessage(role, text string) tetherclient.AIMessage {
	return tetherclient.AIMessage{
		Role:  role,
		Parts: []tetherclient.AIContentPart{{Type: "text", Text: text}},
	}
}

// extractText concatenates the text parts of the proxy's output messages.
func extractText(out []tetherclient.AIMessage) string {
	var b strings.Builder
	for _, m := range out {
		for _, p := range m.Parts {
			if p.Type == "text" && p.Text != "" {
				b.WriteString(p.Text)
			}
		}
	}
	return b.String()
}

func mapUsage(u tetherclient.AIUsage) *llmtypes.Usage {
	if u == (tetherclient.AIUsage{}) {
		return nil
	}
	return &llmtypes.Usage{
		InputTokens:     u.InputTokens,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: u.CacheReadTokens,
	}
}

// emit sends ev unless ctx is done (avoids blocking on an abandoned consumer).
func emit(ctx context.Context, out chan<- llmtypes.StreamEvent, ev llmtypes.StreamEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}
