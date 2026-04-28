//go:build e2e_mux

package muxproxy

import (
	"context"
	"os"
	"testing"
	"time"

	agentmux "github.com/hollis-labs/go-agentmux-client"
)

// TestE2E_LaunchSendStop exercises the real agent-mux daemon. Skipped
// unless built with -tags=e2e_mux AND AGENTMUX_LAUNCH_ID is set to a
// claudestream-kind launch that the daemon knows about.
// POC — CW-20260420-0047.
func TestE2E_LaunchSendStop(t *testing.T) {
	launchID := os.Getenv("AGENTMUX_LAUNCH_ID")
	if launchID == "" {
		t.Skip("AGENTMUX_LAUNCH_ID not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := Client()
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("daemon health check failed: %v", err)
	}

	resp, err := c.Launch(ctx, launchID)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() { _ = c.StopSession(context.Background(), resp.ID) })

	if err := c.SendInput(ctx, resp.ID, []byte("say hello\n")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	events, errs := c.StreamEvents(ctx, agentmux.StreamEventsOptions{
		SessionID: resp.ID,
		Scopes:    []string{"session"},
	})
	select {
	case <-events:
	case err := <-errs:
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}
