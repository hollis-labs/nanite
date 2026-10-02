package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/go-envelopes/admin"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func adminFixture(t *testing.T) (*API, *store.Store, http.Handler) {
	t.Helper()
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := st.Close(ctx); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if seedErr := st.Seed(ctx); seedErr != nil {
		t.Fatal(seedErr)
	}
	a := &API{Services: &service.Container{Settings: service.NewUserSettingsService(st), ProcessTracker: chat.NewProcessTracker()}}
	handler, handlerErr := a.NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"))
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	return a, st, handler
}

func adminRequest(handler http.Handler, method, path string, body io.Reader, authenticated bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if authenticated {
		request.SetBasicAuth("operator", "fixture-password")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func adminDecode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatal("unsafe response headers")
	}
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAdminReadOnlyDiscovery(t *testing.T) {
	a, st, handler := adminFixture(t)
	before, err := st.GetAdminPreferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest := adminDecode[admin.Manifest](t, adminRequest(handler, "GET", "/api/admin/manifest", nil, true))
	if manifest.App.ID != "nanite" || manifest.ContractVersion != 1 || len(manifest.Settings) != 1 {
		t.Fatal("wrong app contract")
	}
	group := manifest.Settings[0]
	if group.ID != "preferences" || group.Scope != (admin.Scope{Kind: "app", ID: "nanite"}) || group.Capabilities != (admin.Capabilities{CanRead: true}) {
		t.Fatal("wrong read-only scope")
	}
	if group.Endpoints.Read == nil || group.Endpoints.Read.Path != "/api/admin/settings/preferences" || group.Endpoints.Validate != nil || group.Endpoints.Update != nil || group.Endpoints.Reset != nil {
		t.Fatal("mutation advertised")
	}
	if !reflect.DeepEqual(group.Resolution.Precedence, []admin.SourceKind{admin.OverrideSource}) || group.Resolution.WriteLayer != "" {
		t.Fatal("invented fallback/write layer")
	}
	if len(group.Fields) != 2 || len(group.Schema.Properties) != 2 {
		t.Fatal("non-whitelisted settings exposed")
	}
	for _, key := range []string{"tool_stream_behavior", "tool_drawer_retention"} {
		field, exists := group.Fields[key]
		if !exists || field.Editable || field.Secret || field.RestartRequired || field.ApplyTarget != "" || field.ReadOnlyReason == "" {
			t.Fatalf("dishonest declaration %s", key)
		}
		property := group.Schema.Properties[key]
		if !property.ReadOnly || property.Default != nil {
			t.Fatal("invented default")
		}
	}
	expectedStream := []any{"streaming", "persist", "hidden"}
	actualStream := []any{}
	for _, value := range group.Schema.Properties["tool_stream_behavior"].Enum {
		actualStream = append(actualStream, value.Value())
	}
	if !reflect.DeepEqual(actualStream, expectedStream) {
		t.Fatal("wrong stream enum")
	}
	expectedRetention := []any{float64(-1), float64(5), float64(15), float64(30), float64(60)}
	actualRetention := []any{}
	for _, value := range group.Schema.Properties["tool_drawer_retention"].Enum {
		actualRetention = append(actualRetention, value.Value())
	}
	if !reflect.DeepEqual(actualRetention, expectedRetention) {
		t.Fatal("wrong retention enum")
	}
	if len(manifest.Health) != 1 || len(manifest.Stats) != 1 || len(manifest.Series) != 0 || len(manifest.Diagnostics) != 0 {
		t.Fatal("unexpected resources")
	}
	// A broken value provider does not affect metadata discovery or initialize it.
	a.Services.Settings = nil
	if _, execErr := st.DB.Exec(`UPDATE user_settings SET admin_preferences_version='' WHERE id=1`); execErr != nil {
		t.Fatal(execErr)
	}
	adminDecode[admin.Manifest](t, adminRequest(handler, "GET", "/api/admin/manifest", nil, true))
	response := adminRequest(handler, "GET", "/api/admin/settings/preferences", nil, true)
	if response.Code != 503 {
		t.Fatal("missing settings provider synthesized values")
	}
	var version string
	if scanErr := st.DB.QueryRow(`SELECT admin_preferences_version FROM user_settings WHERE id=1`).Scan(&version); scanErr != nil {
		t.Fatal(scanErr)
	}
	if version != "" || before.Version == "" {
		t.Fatal("discovery wrote bootstrap metadata")
	}
}

func TestAdminRealStoreETagAndPOSTDenial(t *testing.T) {
	_, st, handler := adminFixture(t)
	ctx := context.Background()
	read := func() (*httptest.ResponseRecorder, admin.Snapshot) {
		response := adminRequest(handler, "GET", "/api/admin/settings/preferences", nil, true)
		return response, adminDecode[admin.Snapshot](t, response)
	}
	initial, snapshot := read()
	oldETag := initial.Header().Get("ETag")
	preferences, err := st.GetAdminPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if oldETag != `"`+preferences.Version+`"` || strings.HasPrefix(oldETag, "W/") {
		t.Fatal("ETag not strong host generation")
	}
	for _, record := range snapshot.Values {
		if record.Editable || !record.HasOverride || record.Source == nil || record.Source.Kind != admin.OverrideSource || record.ApplyState != admin.UnknownApply || record.ReadOnlyReason == "" {
			t.Fatal("dishonest value metadata")
		}
	}
	if len(snapshot.Values) != 2 || !snapshot.Validation.Valid {
		t.Fatal("bad snapshot")
	}
	legacy, legacyErr := st.GetUserSettings(ctx)
	if legacyErr != nil {
		t.Fatal(legacyErr)
	}
	if updateErr := st.UpdateUserSettings(ctx, legacy); updateErr != nil {
		t.Fatal(updateErr)
	}
	same, _ := read()
	if same.Header().Get("ETag") != oldETag {
		t.Fatal("no-op token churn")
	}
	legacy.ToolStreamBehavior = "hidden"
	if updateErr := st.UpdateUserSettings(ctx, legacy); updateErr != nil {
		t.Fatal(updateErr)
	}
	changed, updated := read()
	if changed.Header().Get("ETag") == oldETag || updated.Values["tool_stream_behavior"].Value.Value() != "hidden" {
		t.Fatal("legacy write not reflected atomically")
	}
	legacy.ToolDrawerRetention = 30
	if updateErr := st.UpdateUserSettings(ctx, legacy); updateErr != nil {
		t.Fatal(updateErr)
	}
	changedAgain, updatedAgain := read()
	if changedAgain.Header().Get("ETag") == changed.Header().Get("ETag") || updatedAgain.Values["tool_drawer_retention"].Value.Value() != float64(30) {
		t.Fatal("retention change not invalidated")
	}
	before, readErr := st.GetAdminPreferences(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, path := range []string{"/api/admin", "/api/admin/manifest", "/api/admin/settings/preferences", "/api/admin/settings/preferences/validate", "/api/admin/settings/preferences/update", "/api/admin/settings/preferences/reset", "/api/admin/health/cli-process-activity", "/api/admin/unknown"} {
		body := &adminUnreadBody{}
		request := httptest.NewRequest("POST", path, body)
		request.SetBasicAuth("operator", "fixture-password")
		request.Header.Set("If-Match", oldETag)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 403 || body.reads != 0 {
			t.Fatalf("POST read or admitted %s: %d reads=%d", path, response.Code, body.reads)
		}
		var failure admin.ErrorResponse
		if decodeErr := json.Unmarshal(response.Body.Bytes(), &failure); decodeErr != nil || failure.Error.Code != admin.Forbidden {
			t.Fatal("unsafe POST failure")
		}
	}
	after, afterErr := st.GetAdminPreferences(ctx)
	if afterErr != nil {
		t.Fatal(afterErr)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("POST changed settings")
	}
	backend := adminPreferencesBackend{}
	if _, previewErr := backend.Preview(ctx, admin.Changes{}); previewErr == nil {
		t.Fatal("preview unexpectedly supported")
	}
	if transactionErr := backend.WithTransaction(ctx, func(admin.GroupTransaction) error { t.Fatal("transaction callback ran"); return nil }); transactionErr == nil {
		t.Fatal("transaction unexpectedly supported")
	}
	if _, execErr := st.DB.Exec(`UPDATE user_settings SET admin_preferences_version='' WHERE id=1`); execErr != nil {
		t.Fatal(execErr)
	}
	response := adminRequest(handler, "GET", "/api/admin/settings/preferences", nil, true)
	if response.Code != 503 || response.Header().Get("ETag") != "" || strings.Contains(response.Body.String(), "version") {
		t.Fatal("missing version failed open/leaked error")
	}
}

type adminUnreadBody struct{ reads int }

func (b *adminUnreadBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("body must never be read")
}

func TestAdminAuthBeforeLookup(t *testing.T) {
	for _, cfg := range []struct{ name, user, password string }{{"off", "", ""}, {"user-only", "operator", ""}, {"password-only", "", "fixture-password"}, {"complete", "operator", "fixture-password"}} {
		t.Run(cfg.name, func(t *testing.T) {
			a := &API{}
			handler, err := a.NewAdminHandler(NewBasicWorkflowResponderAuthenticator(cfg.user, cfg.password))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/admin/manifest", "/api/admin/settings/preferences", "/api/admin/unknown"} {
				for _, credentials := range []string{"none", "wrong", "configured"} {
					request := httptest.NewRequest("GET", path, nil)
					request.Header.Set("X-Nanite-Caller-Agent", "operator")
					request.Header.Set("X-Nanite-Caller-Session", "operator")
					switch credentials {
					case "configured":
						request.SetBasicAuth(cfg.user, cfg.password)
					case "wrong":
						request.SetBasicAuth("operator", "wrong")
					}
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					allowed := cfg.user != "" && cfg.password != "" && credentials == "configured"
					if !allowed {
						if response.Code != 401 || response.Header().Get("WWW-Authenticate") != `Basic realm="nanite"` || strings.Contains(response.Body.String(), "preferences") {
							t.Fatal("unauthenticated lookup leaked")
						}
					} else if path == "/api/admin/manifest" {
						adminDecode[admin.Manifest](t, response)
					} else if path == "/api/admin/unknown" {
						if response.Code != 404 {
							t.Fatal("authorized unknown route")
						}
					} else if response.Code != 503 {
						t.Fatal("absent provider failed open")
					}
				}
			}
		})
	}
	handler, err := (&API{}).NewAdminHandler(nil)
	if err != nil {
		t.Fatal(err)
	}
	if response := adminRequest(handler, "GET", "/api/admin/manifest", nil, true); response.Code != 401 {
		t.Fatal("nil policy failed open")
	}
}

func TestAdminObservationsTruthAndPrivacy(t *testing.T) {
	a, _, handler := adminFixture(t)
	health := adminDecode[admin.HealthObservation](t, adminRequest(handler, "GET", "/api/admin/health/cli-process-activity", nil, true))
	count := adminDecode[admin.StatObservation](t, adminRequest(handler, "GET", "/api/admin/stats/cli-process-count", nil, true))
	if health.Status != admin.Healthy || count.Value == nil || *count.Value != 0 {
		t.Fatal("real empty tracker observation incorrect")
	}
	process := &os.Process{Pid: 987654321}
	a.Services.ProcessTracker.Track("PRIVATE_SESSION", process)
	response := adminRequest(handler, "GET", "/api/admin/health/cli-process-activity", nil, true)
	health = adminDecode[admin.HealthObservation](t, response)
	if strings.Contains(response.Body.String(), "PRIVATE_SESSION") || strings.Contains(response.Body.String(), "987654321") {
		t.Fatal("private process identity leaked")
	}
	count = adminDecode[admin.StatObservation](t, adminRequest(handler, "GET", "/api/admin/stats/cli-process-count", nil, true))
	if count.Value == nil || *count.Value != 1 {
		t.Fatal("count not tracker-backed")
	}
	stale := adminProcessActivity([]chat.ProcessHealth{{SessionID: "PRIVATE_SESSION", PID: 987654321, IsStale: true}})
	if stale.Status != admin.Degraded || !strings.Contains(stale.Checks[0].Message, "without output") {
		t.Fatal("staleness misrepresented as liveness")
	}
	encoded, encodeErr := json.Marshal(stale)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if strings.Contains(string(encoded), "PRIVATE_SESSION") || strings.Contains(string(encoded), "987654321") {
		t.Fatal("aggregate leaked identity")
	}
	if validateErr := stale.Validate(); validateErr != nil {
		t.Fatal(validateErr)
	}
	a.Services.ProcessTracker = nil
	for _, path := range []string{"/api/admin/health/cli-process-activity", "/api/admin/stats/cli-process-count"} {
		if unavailable := adminRequest(handler, "GET", path, nil, true); unavailable.Code != 503 || strings.Contains(unavailable.Body.String(), "healthy") {
			t.Fatal("unavailable provider fabricated sample")
		}
	}
	absent, handlerErr := a.NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"))
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	absentManifest := adminDecode[admin.Manifest](t, adminRequest(absent, "GET", "/api/admin/manifest", nil, true))
	presentManifest := adminDecode[admin.Manifest](t, adminRequest(handler, "GET", "/api/admin/manifest", nil, true))
	if len(absentManifest.Health) != 0 || len(absentManifest.Stats) != 0 || absentManifest.Revision == presentManifest.Revision {
		t.Fatal("absent provider declaration dishonest")
	}
}

func TestAdminConcurrentTrackerReads(t *testing.T) {
	a := &API{Services: &service.Container{ProcessTracker: chat.NewProcessTracker()}}
	handler, err := a.NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(pid int) {
			defer workers.Done()
			process := &os.Process{Pid: pid}
			for iteration := 0; iteration < 25; iteration++ {
				a.Services.ProcessTracker.Track("fixture", process)
				a.Services.ProcessTracker.Touch("fixture", pid)
				for _, path := range []string{"/api/admin/health/cli-process-activity", "/api/admin/stats/cli-process-count"} {
					response := adminRequest(handler, "GET", path, nil, true)
					if response.Code != 200 {
						t.Errorf("concurrent read failed: %d", response.Code)
					}
				}
				a.Services.ProcessTracker.Untrack("fixture", process)
			}
		}(worker + 10000000)
	}
	workers.Wait()
	if a.Services.ProcessTracker.Count() != 0 {
		t.Fatal("tracker entries not removed")
	}
}

func TestAdminBackendErrorRedaction(t *testing.T) {
	a := &API{Services: &service.Container{Settings: service.NewUserSettingsService(adminFailingStore{})}}
	handler, err := a.NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"))
	if err != nil {
		t.Fatal(err)
	}
	response := adminRequest(handler, "GET", "/api/admin/settings/preferences", nil, true)
	if response.Code != 503 || strings.Contains(response.Body.String(), "SECRET") || strings.Contains(response.Body.String(), "/private/") {
		t.Fatal("raw backend failure leaked")
	}
}

type adminFailingStore struct{}

func (adminFailingStore) GetUserSettings(context.Context) (*store.UserSettings, error) {
	return nil, errors.New("unused")
}
func (adminFailingStore) UpdateUserSettings(context.Context, *store.UserSettings) error {
	return errors.New("unused")
}
func (adminFailingStore) GetAdminPreferences(context.Context) (*store.AdminPreferences, error) {
	return nil, errors.New("SECRET at /private/database")
}

// The HTTP snapshot must pair each generation with exactly the values minted
// in the writer's transaction, even while readers and a legacy SQL writer run.
func TestAdminConcurrentPreferenceReads(t *testing.T) {
	_, st, handler := adminFixture(t)
	type pair struct {
		stream    string
		retention float64
	}
	initialResponse := adminRequest(handler, "GET", "/api/admin/settings/preferences", nil, true)
	initial := adminDecode[admin.Snapshot](t, initialResponse)
	known := map[string]pair{initialResponse.Header().Get("ETag"): {initial.Values["tool_stream_behavior"].Value.Value().(string), initial.Values["tool_drawer_retention"].Value.Value().(float64)}}
	type sample struct {
		etag  string
		value pair
	}
	samples := make(chan sample, 200)
	writerDone := make(chan struct{})
	var readers sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for iteration := 0; iteration < 50; iteration++ {
				response := adminRequest(handler, "GET", "/api/admin/settings/preferences", nil, true)
				if response.Code != 200 {
					t.Errorf("concurrent settings status=%d", response.Code)
					return
				}
				var snapshot admin.Snapshot
				if decodeErr := json.Unmarshal(response.Body.Bytes(), &snapshot); decodeErr != nil {
					t.Error(decodeErr)
					return
				}
				samples <- sample{response.Header().Get("ETag"), pair{snapshot.Values["tool_stream_behavior"].Value.Value().(string), snapshot.Values["tool_drawer_retention"].Value.Value().(float64)}}
			}
		}()
	}
	go func() {
		defer close(writerDone)
		for iteration := 0; iteration < 50; iteration++ {
			stream := "hidden"
			retention := 30
			if iteration%2 == 0 {
				stream = "persist"
				retention = 15
			}
			if _, execErr := st.DB.Exec(`UPDATE user_settings SET tool_stream_behavior=?,tool_drawer_retention=? WHERE id=1`, stream, retention); execErr != nil {
				t.Error(execErr)
				return
			}
			preferences, readErr := st.GetAdminPreferences(context.Background())
			if readErr != nil {
				t.Error(readErr)
				return
			}
			known[`"`+preferences.Version+`"`] = pair{stream, float64(retention)}
		}
	}()
	readers.Wait()
	<-writerDone
	close(samples)
	for observed := range samples {
		expected, exists := known[observed.etag]
		if !exists || expected != observed.value {
			t.Fatalf("HTTP snapshot paired values with the wrong generation: %+v", observed)
		}
	}
}

func TestAdminUnauthorizedPOSTDoesNotReadBody(t *testing.T) {
	handler, err := (&API{}).NewAdminHandler(NewBasicWorkflowResponderAuthenticator("operator", "fixture-password"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/settings/preferences/update", "/api/admin/unknown"} {
		body := &adminUnreadBody{}
		response := adminRequest(handler, "POST", path, body, false)
		if response.Code != 401 || body.reads != 0 {
			t.Fatal("unauthorized command read/admitted")
		}
	}
}
