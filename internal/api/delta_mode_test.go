package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// Both turn endpoints accept delta_mode and hand it to HandleMessage on the
// request context. That context is the only place it can travel — see
// chat.WithDeltaMode — so the assertion is on what HandleMessage receives.
func TestTurnEndpointsCarryDeltaMode(t *testing.T) {
	a, mux := newTestAPI(t)
	sess := &store.Session{Provider: "anthropic", Model: "claude-sonnet-4", Status: "active"}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var calls int
	var gotMode chat.DeltaMode
	a.Services.Chat = &harnessChatStub{
		handleMessageFn: func(ctx context.Context, _, _ string) (string, error) {
			calls++
			gotMode = chat.DeltaModeFromContext(ctx)
			return "msg-delta-mode", nil
		},
		cancelActiveGenerationFn: func(string) bool { return false },
	}

	endpoints := []struct {
		name, path string
		body       func(deltaField string) string
	}{
		{"messages", "/api/messages", func(f string) string {
			return fmt.Sprintf(`{"session_id":%q,"content":"hi"%s}`, sess.ID, f)
		}},
		{"harness turns", "/api/harness/v1/sessions/" + sess.ID + "/turns", func(f string) string {
			return `{"content":"hi"` + f + `}`
		}},
	}
	modes := []struct {
		name     string
		field    string
		wantCode int
		wantMode chat.DeltaMode
	}{
		{"omitted", ``, http.StatusAccepted, chat.DeltaModePhased},
		{"phased", `,"delta_mode":"phased"`, http.StatusAccepted, chat.DeltaModePhased},
		{"live", `,"delta_mode":"live"`, http.StatusAccepted, chat.DeltaModeLive},
		{"unknown", `,"delta_mode":"lvie"`, http.StatusBadRequest, ""},
	}
	for _, ep := range endpoints {
		for _, m := range modes {
			t.Run(ep.name+"/"+m.name, func(t *testing.T) {
				calls, gotMode = 0, ""
				req := httptest.NewRequest(http.MethodPost, ep.path, bytes.NewReader([]byte(ep.body(m.field))))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)

				if w.Code != m.wantCode {
					t.Fatalf("status = %d, want %d, body=%s", w.Code, m.wantCode, w.Body.String())
				}
				if m.wantCode == http.StatusBadRequest {
					if calls != 0 {
						t.Fatalf("HandleMessage ran %d time(s) for a rejected request", calls)
					}
					if !strings.Contains(w.Body.String(), "delta_mode") {
						t.Fatalf("400 body = %s, want it to name delta_mode", w.Body.String())
					}
					return
				}
				if calls != 1 || gotMode != m.wantMode {
					t.Fatalf("HandleMessage calls=%d mode=%q, want 1 call with mode %q", calls, gotMode, m.wantMode)
				}
			})
		}
	}
}

func TestHarnessV1CapabilitiesAdvertiseDeltaMode(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/harness/v1/capabilities", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("capabilities = %d body=%s", w.Code, w.Body.String())
	}
	var caps harnessV1CapabilitiesResponse
	if err := json.NewDecoder(w.Body).Decode(&caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !containsString(caps.TurnSendFields.Supported, "delta_mode") {
		t.Fatalf("turn send fields = %+v, want delta_mode so adapters can feature-detect it", caps.TurnSendFields)
	}
}
