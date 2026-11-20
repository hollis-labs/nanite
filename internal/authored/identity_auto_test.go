package authored

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/internal/warden"
)

func TestIdentityWiresFailFastGrants(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/token":
					r.Body = http.MaxBytesReader(w, r.Body, 4096)
					if err := r.ParseForm(); err != nil {
						t.Error(err)
						return
					}
					if r.Form.Get("audience") != "warden" {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_scope"})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "parent", "expires_in": 900})
				case "/api/v1/requests":
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "request", "status": "pending"})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			file := filepath.Join(t.TempDir(), "credential")
			if err := tokenfile.Write(file, "parent"); err != nil {
				t.Fatal(err)
			}
			id, err := ParseIdentity([]byte("principal: agent:fixture\nwarden:\n  url: "+srv.URL+"\n  credential_file: "+file+"\n"), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			callbacks := 0
			src, err := NewWardenSource(id, nil, nil, func(warden.Waiting) { callbacks++ }, nil, auto)
			if err != nil {
				t.Fatal(err)
			}
			_, err = src.Token(warden.WithConversation(t.Context(), "chat"), "fixture", nil)
			if err == nil || !strings.Contains(err.Error(), "request") {
				t.Fatalf("error %v", err)
			}
			_, waiting := warden.AsMissingGrant(err)
			if waiting == auto {
				t.Fatalf("auto=%v waiting=%v", auto, waiting)
			}
			want := 1
			if auto {
				want = 0
			}
			if callbacks != want {
				t.Fatalf("callbacks=%d want=%d", callbacks, want)
			}
		})
	}
}
