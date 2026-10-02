package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/go-envelopes/admin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

type observedPreferencesStore struct {
	*store.Store
	callbacks     atomic.Int32
	reads         atomic.Int32
	afterCallback error
	secondRead    func() error
}

func (s *observedPreferencesStore) WithAdminPreferencesTransaction(ctx context.Context, fn func(*store.PreferencesTransaction) error) error {
	return s.Store.WithAdminPreferencesTransaction(ctx, func(tx *store.PreferencesTransaction) error {
		s.callbacks.Add(1)
		if err := fn(tx); err != nil {
			return err
		}
		return s.afterCallback
	})
}
func (s *observedPreferencesStore) GetAdminPreferences(ctx context.Context) (*store.AdminPreferences, error) {
	if s.reads.Add(1) == 2 && s.secondRead != nil {
		if err := s.secondRead(); err != nil {
			return nil, err
		}
	}
	return s.Store.GetAdminPreferences(ctx)
}

func preferencesCommand(h http.Handler, operation, body, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://example.com/api/admin/settings/preferences/"+operation, strings.NewReader(body))
	r.SetBasicAuth("operator", "fixture-password")
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func preferencesRead(t *testing.T, h http.Handler) (admin.Snapshot, string) {
	t.Helper()
	w := adminRequest(h, "GET", "/api/admin/settings/preferences", nil, true)
	return adminDecode[admin.Snapshot](t, w), w.Header().Get("ETag")
}
func preferencesBody(revision, set, unset string) string {
	return `{"revision":"` + revision + `","set":` + set + `,"unset":` + unset + `}`
}
func preferencesRow(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM user_settings WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing settings row")
	}
	values := make([]any, len(columns))
	ptr := make([]any, len(columns))
	for i := range ptr {
		ptr[i] = &values[i]
	}
	if err := rows.Scan(ptr...); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{}
	for i, key := range columns {
		result[key] = values[i]
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func observedHandler(t *testing.T, a *API, s *observedPreferencesStore) http.Handler {
	t.Helper()
	a.Services.Settings = service.NewUserSettingsService(s)
	h, err := a.NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func TestAdminPreferenceCommandsAndReset(t *testing.T) {
	_, st, h := adminFixture(t)
	if _, err := st.DB.Exec(`UPDATE user_settings SET updated_at='2001-01-01' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	initial, etag := preferencesRead(t, h)
	before := preferencesRow(t, st.DB)
	body := preferencesBody(initial.Revision, `{"tool_stream_behavior":"hidden","tool_drawer_retention":60}`, `[]`)
	validation := preferencesCommand(h, "validate", body, "")
	if !adminDecode[admin.Validation](t, validation).Valid || !reflect.DeepEqual(before, preferencesRow(t, st.DB)) {
		t.Fatal("preview wrote or invalid validation")
	}
	update := preferencesCommand(h, "update", body, etag)
	result := adminDecode[admin.UpdateResponse](t, update)
	if !reflect.DeepEqual(result.ChangedKeys, []string{"tool_stream_behavior", "tool_drawer_retention"}) || result.RestartRequired || len(result.ApplyTargets) != 0 || update.Header().Get("ETag") == etag {
		t.Fatal("bad update envelope")
	}
	for _, v := range result.Snapshot.Values {
		if !v.Editable || !v.HasOverride || v.Source.Kind != admin.OverrideSource || v.ApplyState != admin.UnknownApply {
			t.Fatal("bad override/apply projection")
		}
	}
	after := preferencesRow(t, st.DB)
	if after["updated_at"] == before["updated_at"] {
		t.Fatal("actual change retained updated_at")
	}
	for _, key := range []string{"tool_stream_behavior", "tool_drawer_retention", "updated_at", "admin_preferences_version"} {
		delete(before, key)
		delete(after, key)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("admin changed unrelated singleton columns (values omitted)")
	}
	snapshot, committed := preferencesRead(t, h)
	if committed != update.Header().Get("ETag") || !reflect.DeepEqual(snapshot, result.Snapshot) {
		t.Fatal("response does not match committed read")
	}
	reset := preferencesCommand(h, "reset", `{"revision":"`+initial.Revision+`","keys":["tool_stream_behavior"]}`, committed)
	resetResult := adminDecode[admin.UpdateResponse](t, reset)
	v := resetResult.Snapshot.Values["tool_stream_behavior"]
	if v.Value.Value() != "streaming" || v.HasOverride || v.Source.Kind != admin.DefaultSource || resetResult.Snapshot.Values["tool_drawer_retention"].Value.Value() != float64(60) || !reflect.DeepEqual(resetResult.ChangedKeys, []string{"tool_stream_behavior"}) {
		t.Fatal("reset not keyed/canonical")
	}
	noopBefore := preferencesRow(t, st.DB)
	for _, tc := range []struct{ op, body string }{
		{"reset", `{"revision":"` + initial.Revision + `","keys":["tool_stream_behavior"]}`},
		{"reset", `{"revision":"` + initial.Revision + `","keys":[]}`},
		{"update", preferencesBody(initial.Revision, `{"tool_stream_behavior":"streaming"}`, `[]`)},
	} {
		w := preferencesCommand(h, tc.op, tc.body, reset.Header().Get("ETag"))
		r := adminDecode[admin.UpdateResponse](t, w)
		if w.Header().Get("ETag") != reset.Header().Get("ETag") || len(r.ChangedKeys) != 0 || r.RestartRequired || len(r.ApplyTargets) != 0 || !reflect.DeepEqual(noopBefore, preferencesRow(t, st.DB)) {
			t.Fatal("default set/reset no-op changed row or token")
		}
	}
	mixed := preferencesCommand(h, "update", preferencesBody(initial.Revision, `{"tool_stream_behavior":"persist"}`, `["tool_drawer_retention"]`), reset.Header().Get("ETag"))
	mixedResult := adminDecode[admin.UpdateResponse](t, mixed)
	if mixedResult.Snapshot.Values["tool_stream_behavior"].Value.Value() != "persist" || mixedResult.Snapshot.Values["tool_drawer_retention"].Value.Value() != float64(15) || mixedResult.Snapshot.Values["tool_drawer_retention"].HasOverride {
		t.Fatal("combined candidate reset failed")
	}
}

func TestAdminPreferenceNegativeControls(t *testing.T) {
	_, st, h := adminFixture(t)
	snapshot, etag := preferencesRead(t, h)
	beforeInvalid := preferencesRow(t, st.DB)
	invalid := preferencesCommand(h, "validate", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"bad"}`, `[]`), "")
	if adminDecode[admin.Validation](t, invalid).Valid || !reflect.DeepEqual(beforeInvalid, preferencesRow(t, st.DB)) {
		t.Fatal("invalid preview admitted or persisted")
	}
	good := preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"hidden"}`, `[]`)
	for _, tc := range []struct {
		name, op, body, tag string
		status              int
	}{
		{"missing-precondition", "update", good, "", 428},
		{"stale", "update", good, `"stale"`, 412},
		{"weak", "update", good, "W/" + etag, 412},
		{"old-manifest", "update", preferencesBody("old", `{"tool_stream_behavior":"hidden"}`, `[]`), `"stale"`, 409},
		{"old-manifest-unknown-key", "update", preferencesBody("old", `{"secret":"x"}`, `[]`), etag, 400},
		{"malformed", "update", `{`, etag, 400},
		{"unknown-property", "update", strings.TrimSuffix(good, "}") + `,"unknown":true}`, etag, 400},
		{"unknown-key", "update", preferencesBody(snapshot.Revision, `{"secret":"x"}`, `[]`), etag, 400},
		{"enum", "update", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"bad"}`, `[]`), etag, 422},
		{"integer-enum", "update", preferencesBody(snapshot.Revision, `{"tool_drawer_retention":10}`, `[]`), etag, 422},
		{"fractional", "update", preferencesBody(snapshot.Revision, `{"tool_drawer_retention":15.5}`, `[]`), etag, 400},
		{"rounded-fractional", "update", preferencesBody(snapshot.Revision, `{"tool_drawer_retention":15.0000000000000001}`, `[]`), etag, 400},
		{"required-wrong-type", "update", preferencesBody(snapshot.Revision, `{"tool_drawer_retention":"15"}`, `[]`), etag, 400},
		{"duplicate-reset", "reset", `{"revision":"` + snapshot.Revision + `","keys":["tool_stream_behavior","tool_stream_behavior"]}`, etag, 400},
		{"unknown-reset", "reset", `{"revision":"` + snapshot.Revision + `","keys":["secret"]}`, etag, 400},
		{"intersection", "update", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"hidden"}`, `["tool_stream_behavior"]`), etag, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := preferencesRow(t, st.DB)
			w := preferencesCommand(h, tc.op, tc.body, tc.tag)
			if w.Code != tc.status || w.Header().Get("ETag") != "" {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			if !reflect.DeepEqual(before, preferencesRow(t, st.DB)) {
				t.Fatal("rejected command persisted")
			}
		})
	}
}

func TestAdminPreferenceConcurrentCAS(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "updates", true: "update-reset"}[reset], func(t *testing.T) {
			a, st, _ := adminFixture(t)
			if reset {
				if _, err := st.DB.Exec(`UPDATE user_settings SET tool_stream_behavior='persist' WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			}
			observed := &observedPreferencesStore{Store: st}
			h := observedHandler(t, a, observed)
			snapshot, etag := preferencesRead(t, h)
			start := make(chan struct{})
			responses := make(chan *httptest.ResponseRecorder, 2)
			for i := range 2 {
				go func() {
					<-start
					op, body := "update", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"hidden"}`, `[]`)
					if i == 1 {
						body = preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"persist"}`, `[]`)
						if reset {
							op = "reset"
							body = `{"revision":"` + snapshot.Revision + `","keys":["tool_stream_behavior"]}`
						}
					}
					responses <- preferencesCommand(h, op, body, etag)
				}()
			}
			close(start)
			success, conflict := 0, 0
			tag := ""
			for range 2 {
				w := <-responses
				switch w.Code {
				case 200:
					success++
					tag = w.Header().Get("ETag")
					adminDecode[admin.UpdateResponse](t, w)
				case 412:
					conflict++
				default:
					t.Fatalf("race status=%d body=%s", w.Code, w.Body.String())
				}
			}
			if success != 1 || conflict != 1 || observed.callbacks.Load() != 2 {
				t.Fatal("CAS failed or callback replayed")
			}
			_, committed := preferencesRead(t, h)
			if tag != committed {
				t.Fatal("success token not committed")
			}
		})
	}
}

func TestAdminPreferencePostStageRollbackAndRedaction(t *testing.T) {
	a, st, _ := adminFixture(t)
	observed := &observedPreferencesStore{Store: st, afterCallback: errors.New("SECRET at /private/database")}
	h := observedHandler(t, a, observed)
	snapshot, etag := preferencesRead(t, h)
	before := preferencesRow(t, st.DB)
	w := preferencesCommand(h, "update", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"hidden"}`, `[]`), etag)
	if w.Code != 503 || w.Header().Get("ETag") != "" || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "/private/") || strings.Contains(w.Body.String(), "snapshot") {
		t.Fatal("post-stage failure released staged response/error")
	}
	if observed.callbacks.Load() != 1 || !reflect.DeepEqual(before, preferencesRow(t, st.DB)) {
		t.Fatal("post-stage failure did not roll back whole row/version")
	}
}

func TestAdminPreferenceValidateRaceIsUnavailable(t *testing.T) {
	a, st, _ := adminFixture(t)
	snapshot, _ := preferencesRead(t, mustAdminHandler(t, a))
	observed := &observedPreferencesStore{Store: st, secondRead: func() error {
		_, err := st.DB.Exec(`UPDATE user_settings SET tool_stream_behavior='hidden' WHERE id=1`)
		return err
	}}
	h := observedHandler(t, a, observed)
	w := preferencesCommand(h, "validate", preferencesBody(snapshot.Revision, `{"tool_drawer_retention":30}`, `[]`), "")
	if w.Code != 503 || observed.callbacks.Load() != 0 {
		t.Fatal("preview race falsely validated or started transaction")
	}
	p, err := st.GetAdminPreferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.ToolStreamBehavior != "hidden" || p.ToolDrawerRetention != 15 {
		t.Fatal("preview persisted candidate")
	}
}
func mustAdminHandler(t *testing.T, a *API) http.Handler {
	t.Helper()
	h, err := a.NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Blocks body decoding AFTER the actual legacy handler has read its whole row.
type legacyDelayedBody struct {
	reader        io.Reader
	once          sync.Once
	read, release chan struct{}
}

func (b *legacyDelayedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.read); <-b.release })
	return b.reader.Read(p)
}

func TestAdminPreferenceLegacyWriterBoundary(t *testing.T) {
	a, st, h := adminFixture(t)
	snapshot, etag := preferencesRead(t, h)
	legacy := adminRequest(http.HandlerFunc(a.handleUpdateSettings), "PUT", "/api/settings", strings.NewReader(`{"tool_stream_behavior":"persist"}`), true)
	if legacy.Code != 200 {
		t.Fatalf("legacy write: %d", legacy.Code)
	}
	w := preferencesCommand(h, "update", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"hidden"}`, `[]`), etag)
	if w.Code != 412 {
		t.Fatal("legacy writer did not invalidate old admin token")
	}
	// Reproduce the accepted limit: a stale legacy row commits after admin success.
	snapshot, etag = preferencesRead(t, h)
	delayed := &legacyDelayedBody{reader: strings.NewReader(`{"default_model":"unrelated-model"}`), read: make(chan struct{}), release: make(chan struct{})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- adminRequest(http.HandlerFunc(a.handleUpdateSettings), "PUT", "/api/settings", delayed, true)
	}()
	<-delayed.read
	changed := preferencesCommand(h, "update", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"hidden"}`, `[]`), etag)
	adminDecode[admin.UpdateResponse](t, changed)
	close(delayed.release)
	if (<-done).Code != 200 {
		t.Fatal("stale legacy write failed")
	}
	final, finalTag := preferencesRead(t, h)
	if final.Values["tool_stream_behavior"].Value.Value() != "persist" || finalTag == changed.Header().Get("ETag") {
		t.Fatal("legacy overwrite boundary changed or failed to invalidate")
	}
	t.Log("accepted limit reproduced: stale legacy whole-row PUT after admin success overwrites the preference and invalidates its ETag")
	// Use fresh stored value, not the request's original intention, for reconciliation.
	p, err := st.GetAdminPreferences(context.Background())
	if err != nil || p.ToolStreamBehavior != "persist" {
		t.Fatal("legacy persistence mismatch")
	}
}

func TestAdminPreferenceCommandBackendErrorRedaction(t *testing.T) {
	a := &API{Services: &service.Container{Settings: service.NewUserSettingsService(adminFailingStore{})}}
	h := mustAdminHandler(t, a)
	w := preferencesCommand(h, "update", preferencesBody("nanite.admin.v2.preferences", `{}`, `[]`), `"stale"`)
	var failure admin.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if w.Code != 503 || failure.Error.Code != admin.BackendUnavailable || strings.Contains(w.Body.String(), "SECRET") || w.Header().Get("ETag") != "" {
		t.Fatal("unsafe transaction error")
	}
}

func TestAdminPreferenceConcurrentNoOps(t *testing.T) {
	a, st, _ := adminFixture(t)
	counted := &observedPreferencesStore{Store: st}
	h := observedHandler(t, a, counted)
	snapshot, etag := preferencesRead(t, h)
	before := preferencesRow(t, st.DB)
	start := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			<-start
			done <- preferencesCommand(h, "update", preferencesBody(snapshot.Revision, `{"tool_stream_behavior":"streaming"}`, `[]`), etag)
		}()
	}
	close(start)
	for range 2 {
		w := <-done
		result := adminDecode[admin.UpdateResponse](t, w)
		if w.Header().Get("ETag") != etag || len(result.ChangedKeys) != 0 {
			t.Fatal("no-op churned token")
		}
	}
	if counted.callbacks.Load() != 2 || !reflect.DeepEqual(before, preferencesRow(t, st.DB)) {
		t.Fatal("no-op callback replay/persistence")
	}
}
