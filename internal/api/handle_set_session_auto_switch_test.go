package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestHandleSetSessionAutoSwitch_RequiresOverrideKey verifies that omitting
// the `override` field returns 400 instead of silently clearing the
// per-session override (PR #93 Copilot feedback on F2 / CW-20260429-0002).
// `encoding/json` decodes both `{}` and `{"override": null}` into a nil
// pointer; the handler now decodes via raw map and rejects absent keys.
func TestHandleSetSessionAutoSwitch_RequiresOverrideKey(t *testing.T) {
	a, mux := newTestAPI(t)

	ws := &store.Workspace{ID: "f2pk-ws", Name: "F2 PR93"}
	if err := a.Services.Store.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{
		ID:          "f2pk-sess",
		WorkspaceID: ws.ID,
		Title:       "F2 absent-key test",
	}
	if err := a.Services.Store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty object", `{}`, http.StatusBadRequest},
		{"unrelated field only", `{"foo":"bar"}`, http.StatusBadRequest},
		{"explicit null clears", `{"override":null}`, http.StatusOK},
		{"explicit true sets", `{"override":true}`, http.StatusOK},
		{"explicit false sets", `{"override":false}`, http.StatusOK},
		{"non-bool 400", `{"override":"yes"}`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch,
				"/api/sessions/"+sess.ID+"/auto-switch",
				bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}

			if tc.wantStatus == http.StatusOK {
				var resp SessionAutoSwitchResponse
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				// Round-trip: response Override should match the request value.
				switch tc.name {
				case "explicit null clears":
					if resp.Override != nil {
						t.Errorf("override = %v, want nil", *resp.Override)
					}
				case "explicit true sets":
					if resp.Override == nil || *resp.Override != true {
						t.Errorf("override = %v, want true", resp.Override)
					}
				case "explicit false sets":
					if resp.Override == nil || *resp.Override != false {
						t.Errorf("override = %v, want false", resp.Override)
					}
				}
			}
		})
	}
}
