package muxproxy

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeMuxService struct {
	listCalled   bool
	launchCalled bool
	sendCalled   bool
	stopCalled   bool
}

func (f *fakeMuxService) ListAvailableLaunches(_ context.Context) ([]LaunchSummary, error) {
	f.listCalled = true
	return []LaunchSummary{{ID: "lx-1", Provider: "claudestream"}}, nil
}

func (f *fakeMuxService) LaunchSubordinate(_ context.Context, launchID, nickname string) (LaunchResult, error) {
	f.launchCalled = true
	return LaunchResult{SessionID: "sess-A", ProviderID: "claudestream", Nickname: nickname}, nil
}

func (f *fakeMuxService) Send(_ context.Context, sessionID, text string) (SendResult, error) {
	f.sendCalled = true
	return SendResult{Transcript: "ack: " + text, ExitStatus: "done"}, nil
}

func (f *fakeMuxService) Stop(_ context.Context, sessionID string) error {
	f.stopCalled = true
	return nil
}

func TestTransport_ListTools(t *testing.T) {
	tr := NewTransport(&fakeMuxService{})
	tools, err := tr.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mux_list_launches", "mux_launch", "mux_send", "mux_stop"}
	if len(tools) != len(want) {
		t.Fatalf("want %d tools, got %d", len(want), len(tools))
	}
	names := map[string]bool{}
	for _, d := range tools {
		names[d.Name] = true
	}
	for _, n := range want {
		if !names[n] {
			t.Fatalf("missing tool %q", n)
		}
	}
}

func TestTransport_CallMuxSend(t *testing.T) {
	f := &fakeMuxService{}
	tr := NewTransport(f)

	raw, err := tr.CallTool(context.Background(), "mux_send", map[string]any{
		"session_id": "sess-A",
		"text":       "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !f.sendCalled {
		t.Fatal("Send was not called")
	}
	var out SendResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Transcript != "ack: hello" {
		t.Fatalf("unexpected transcript %q", out.Transcript)
	}
}

func TestTransport_CallUnknownTool(t *testing.T) {
	tr := NewTransport(&fakeMuxService{})
	_, err := tr.CallTool(context.Background(), "mux_unknown", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
