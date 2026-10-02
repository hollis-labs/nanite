package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/go-envelopes/admin"
	"github.com/hollis-labs/nanite/internal/api"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

type mountedPreferencesStore struct {
	*store.Store
	access atomic.Int32
}

func (s *mountedPreferencesStore) GetAdminPreferences(ctx context.Context) (*store.AdminPreferences, error) {
	s.access.Add(1)
	return s.Store.GetAdminPreferences(ctx)
}
func (s *mountedPreferencesStore) WithAdminPreferencesTransaction(ctx context.Context, fn func(*store.PreferencesTransaction) error) error {
	s.access.Add(1)
	return s.Store.WithAdminPreferencesTransaction(ctx, fn)
}

type mountedUnreadBody struct{ reads int }

func (b *mountedUnreadBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }

func TestAdminMountedPreferencesWriteAndPolicy(t *testing.T) {
	t.Setenv("NANITE_AUTH_USER", "operator")
	t.Setenv("NANITE_AUTH_PASSWORD", "fixture-password")
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "mounted.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(ctx)
	if seedErr := st.Seed(ctx); seedErr != nil {
		t.Fatal(seedErr)
	}
	counted := &mountedPreferencesStore{Store: st}
	a := &api.API{Services: &service.Container{Settings: service.NewUserSettingsService(counted)}}
	s, err := New(st, a, 0, false, nil, config.HTTPConfig{CORSAllowedOrigins: []string{"http://localhost:5173", "*"}})
	if err != nil {
		t.Fatal(err)
	}
	h := s.handlerChain()
	r := httptest.NewRequest("GET", "http://nanite.test/api/admin/settings/preferences", nil)
	r.SetBasicAuth("operator", "fixture-password")
	read := httptest.NewRecorder()
	h.ServeHTTP(read, r)
	var snapshot admin.Snapshot
	if read.Code != 200 {
		t.Fatalf("mounted read %d", read.Code)
	}
	if decodeErr := json.Unmarshal(read.Body.Bytes(), &snapshot); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	body := `{"revision":"` + snapshot.Revision + `","set":{"tool_stream_behavior":"hidden"},"unset":[]}`
	for _, tc := range []struct {
		name          string
		origins       []string
		authenticated bool
		status        int
	}{
		{name: "absent-auth", origins: []string{"http://localhost:5173"}, status: 401},
		{name: "absent-origin", authenticated: true, status: 403},
		{name: "foreign-wildcard", origins: []string{"http://foreign.test"}, authenticated: true, status: 403},
		{name: "null", origins: []string{"null"}, authenticated: true, status: 403},
		{name: "duplicate", origins: []string{"http://localhost:5173", "http://localhost:5173"}, authenticated: true, status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unread := &mountedUnreadBody{}
			request := httptest.NewRequest("POST", "http://nanite.test/api/admin/settings/preferences/update", unread)
			request.Header["Origin"] = tc.origins
			request.Header.Set("If-Match", read.Header().Get("ETag"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Nanite-Caller-Agent", "operator")
			if tc.authenticated {
				request.SetBasicAuth("operator", "fixture-password")
			}
			access := counted.access.Load()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request)
			if w.Code != tc.status || unread.reads != 0 || counted.access.Load() != access {
				t.Fatalf("policy accessed body/store: status=%d reads=%d", w.Code, unread.reads)
			}
		})
	}
	request := httptest.NewRequest("POST", "http://nanite.test/api/admin/settings/preferences/update", strings.NewReader(body))
	request.SetBasicAuth("operator", "fixture-password")
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("If-Match", read.Header().Get("ETag"))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" || w.Header().Get("Access-Control-Expose-Headers") != "ETag" || w.Header().Get("ETag") == read.Header().Get("ETag") {
		t.Fatalf("mounted write failed: %d %s", w.Code, w.Body.String())
	}
	p, err := st.GetAdminPreferences(ctx)
	if err != nil || p.ToolStreamBehavior != "hidden" {
		t.Fatal("mounted write did not persist")
	}
}
