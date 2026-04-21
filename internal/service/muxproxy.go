package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/chrispian/agent-mux/pkg/claudestream"
	agentmux "github.com/hollis-labs/go-agentmux-client"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/muxproxy"
)

// ErrUnsupportedProvider is returned when LaunchSubordinate is invoked
// against a non-claudestream provider. POC is claudestream-only.
var ErrUnsupportedProvider = errors.New("muxproxy: POC supports claudestream providers only")

// muxClient is the subset of *agentmux.Client the service depends on.
// Test seam.
type muxClient interface {
	ListLaunches(ctx context.Context) ([]agentmux.Launch, error)
	Launch(ctx context.Context, launchID string) (agentmux.LaunchResponse, error)
	SendInput(ctx context.Context, sessionID string, data []byte) error
	StopSession(ctx context.Context, sessionID string) error
}


// MuxProxy is the chat-session-facing service layer.
type MuxProxy struct {
	client      muxClient
	mgr         *muxproxy.Manager
	sendTimeout time.Duration
}

// NewMuxProxy constructs a service. Default sendTimeout is 5 minutes.
func NewMuxProxy(c muxClient, mgr *muxproxy.Manager) *MuxProxy {
	return &MuxProxy{
		client:      c,
		mgr:         mgr,
		sendTimeout: 5 * time.Minute,
	}
}

// ListAvailableLaunches returns the daemon's catalog of launches.
func (s *MuxProxy) ListAvailableLaunches(ctx context.Context) ([]muxproxy.LaunchSummary, error) {
	launches, err := s.client.ListLaunches(ctx)
	if err != nil {
		return nil, err
	}
	// Filter to claude-stream launches only. The POC cannot dispatch
	// claude-code or other provider kinds, so hiding them from the LLM
	// prevents it from picking unusable launches and clogging the
	// daemon with failed launch sessions.
	out := make([]muxproxy.LaunchSummary, 0, len(launches))
	for _, l := range launches {
		if l.Provider != "claude-stream" {
			continue
		}
		out = append(out, muxproxy.LaunchSummary{
			ID:       l.ID,
			Project:  l.Project,
			Agent:    l.Agent,
			Provider: l.Provider,
		})
	}
	return out, nil
}

// LaunchSubordinate starts a claudestream-kind subordinate and
// registers its nickname in the Manager.
func (s *MuxProxy) LaunchSubordinate(ctx context.Context, launchID, nickname string) (muxproxy.LaunchResult, error) {
	resp, err := s.client.Launch(ctx, launchID)
	if err != nil {
		return muxproxy.LaunchResult{}, err
	}
	if resp.ProviderID != "claude-stream" {
		// Clean up the orphaned session — without this, every bad
		// launch leaves stale state on muxd and eventually clogs the
		// daemon.
		if resp.ID != "" {
			_ = s.client.StopSession(ctx, resp.ID)
		}
		return muxproxy.LaunchResult{}, fmt.Errorf("%w: got provider_id=%q", ErrUnsupportedProvider, resp.ProviderID)
	}
	chatSessionID := mcp.SessionIDFromContext(ctx)
	s.mgr.Register(chatSessionID, resp.ID, nickname)
	return muxproxy.LaunchResult{
		SessionID:  resp.ID,
		ProviderID: resp.ProviderID,
		Nickname:   nickname,
	}, nil
}

// Send delivers text to the subordinate and blocks until KindDone /
// KindError / timeout.
func (s *MuxProxy) Send(ctx context.Context, sessionID, text string) (muxproxy.SendResult, error) {
	if err := s.client.SendInput(ctx, sessionID, []byte(text+"\n")); err != nil {
		return muxproxy.SendResult{}, err
	}
	ch := s.mgr.WaiterChannel(sessionID)
	if ch == nil {
		return muxproxy.SendResult{}, fmt.Errorf("muxproxy: session %q not registered", sessionID)
	}

	var (
		transcript strings.Builder
		toolUses   []muxproxy.SendToolUse
	)
	deadline := time.NewTimer(s.sendTimeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return muxproxy.SendResult{Transcript: transcript.String(), ToolUses: toolUses, ExitStatus: "error", Error: ctx.Err().Error()}, nil
		case <-deadline.C:
			return muxproxy.SendResult{Transcript: transcript.String(), ToolUses: toolUses, ExitStatus: "timeout"}, nil
		case ev, ok := <-ch:
			if !ok {
				return muxproxy.SendResult{Transcript: transcript.String(), ToolUses: toolUses, ExitStatus: "error", Error: "channel closed"}, nil
			}
			switch ev.Kind {
			case claudestream.KindDelta:
				transcript.WriteString(ev.Text)
			case claudestream.KindToolUse:
				if ev.ToolUse != nil {
					raw, _ := json.Marshal(ev.ToolUse.Input) //nolint:errcheck // map[string]any marshal cannot fail
					toolUses = append(toolUses, muxproxy.SendToolUse{Name: ev.ToolUse.Name, Input: raw})
				}
			case claudestream.KindDone:
				out := muxproxy.SendResult{Transcript: transcript.String(), ToolUses: toolUses, ExitStatus: "done"}
				if ev.Usage != nil {
					out.InputTokens = ev.Usage.InputTokens
					out.OutputTokens = ev.Usage.OutputTokens
				}
				return out, nil
			case claudestream.KindError:
				return muxproxy.SendResult{Transcript: transcript.String(), ToolUses: toolUses, ExitStatus: "error", Error: ev.ErrorMsg}, nil
			case claudestream.KindSessionID, claudestream.KindUsage:
				// informational only; no action needed in the blocking-send path
			}
		}
	}
}

// StopAll terminates every live subordinate. Best-effort — errors are
// logged but not propagated. Used at app shutdown from Manager.Run's
// ctx-cancel path.
func (s *MuxProxy) StopAll(ctx context.Context) {
	ids := s.mgr.StopAll()
	for _, id := range ids {
		if err := s.client.StopSession(ctx, id); err != nil {
			slog.Error("muxproxy: StopSession failed at shutdown", "session", id, "err", err)
		}
	}
}

// Stop kills the subordinate session and unregisters its nickname.
func (s *MuxProxy) Stop(ctx context.Context, sessionID string) error {
	err := s.client.StopSession(ctx, sessionID)
	s.mgr.Unregister(sessionID)
	return err
}
