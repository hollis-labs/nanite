package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// populateScalars sets every string, integer and bool field of the struct v
// points to, leaving other kinds for the caller.
func populateScalars(t *testing.T, v any) {
	t.Helper()
	rv := reflect.ValueOf(v).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(fmt.Sprintf("value-%d", i))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(1000 + i))
		case reflect.Uint64:
			f.SetUint(uint64(2000 + i))
		default:
			// Nested structs and raw payloads are set by the caller.
		}
	}
}

func TestHostRuntimeHeadAndGapViewJSON(t *testing.T) {
	var h store.HostRuntimeHead
	populateScalars(t, &h)
	assertSameJSON(t, "head populated", hostRuntimeHeadToView(h), h)
	assertSameJSON(t, "head zero (current_runtime_run_id omitted)", hostRuntimeHeadToView(store.HostRuntimeHead{}), store.HostRuntimeHead{})

	var g store.HostRuntimeGap
	populateScalars(t, &g)
	assertSameJSON(t, "gap populated", hostRuntimeGapToView(&g), g)
	assertSameJSON(t, "gap zero", hostRuntimeGapToView(&store.HostRuntimeGap{}), store.HostRuntimeGap{})
}

func TestHostRuntimeEventViewJSON(t *testing.T) {
	var e store.HostRuntimeEvent
	populateScalars(t, &e)
	e.Source = store.HostRuntimeEventSource{Channel: "stdout", Confidence: "high"}
	e.Process = store.HostRuntimeProcess{Provider: "p", Runtime: "r", ProviderSessionID: "ps"}
	e.Payload = json.RawMessage(`{"b":2,"a":[1,"x"]}`)
	assertSameJSON(t, "populated", hostRuntimeEventToView(&e), e)

	// Zero: the omitempty fields drop out, "process" stays as {} because
	// omitempty does not drop a struct, and a nil payload is null.
	zero := store.HostRuntimeEvent{}
	got := string(mustJSON(t, hostRuntimeEventToView(&zero)))
	assertSameJSON(t, "zero", hostRuntimeEventToView(&zero), zero)
	for _, want := range []string{`"process":{}`, `"payload":null`} {
		if !strings.Contains(got, want) {
			t.Errorf("zero event %s lacks %s", got, want)
		}
	}
	for _, gone := range []string{"turn_id", "parent_id", "payload_truncated", "confidence", "provider"} {
		if strings.Contains(got, `"`+gone+`"`) {
			t.Errorf("zero event %s carries omitted %s", got, gone)
		}
	}
}

// The feed needs only the runtime feed, not the store handle.
func TestHostRuntimeFeed_GuardsOnTheFeed(t *testing.T) {
	a := &API{Services: &service.Container{}}
	rec := httptest.NewRecorder()
	a.handleHostRuntimeFeed(rec, httptest.NewRequest("GET", "/api/sessions/s/runtime-events", nil))
	if rec.Code != http.StatusServiceUnavailable || errorBody(t, rec) != "host runtime feed not initialized" {
		t.Fatalf("no feed: %d %s", rec.Code, rec.Body.String())
	}

	base, _ := newTestAPI(t)
	a = &API{Services: &service.Container{RuntimeFeed: base.Services.RuntimeFeed}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/sessions/s/runtime-events", nil).WithContext(ctx)
	req.SetPathValue("id", "s")
	rec = httptest.NewRecorder()
	a.handleHostRuntimeFeed(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: host_runtime.head.v1") {
		t.Fatalf("feed without a store handle: %d %s", rec.Code, rec.Body.String())
	}
}

type runtimeCtxKey struct{}

// failingRuntime fails both reads and records whether they ran under the
// caller's context.
type failingRuntime struct{ sawCallerCtx int }

func (f *failingRuntime) GetSessionHalt(ctx context.Context, _ string) (*store.HaltStatus, error) {
	if ctx.Value(runtimeCtxKey{}) != nil {
		f.sawCallerCtx++
	}
	return nil, errors.New("db down")
}

func (f *failingRuntime) ListAgentRuntimeRowsForSession(ctx context.Context, _ string) ([]*store.AgentRuntimeRow, error) {
	if ctx.Value(runtimeCtxKey{}) != nil {
		f.sawCallerCtx++
	}
	return nil, errors.New("db down")
}

// Halt and runtime rows that cannot be read fall back to their defaults, and
// both reads run under the caller's context.
func TestSessionDetails_RuntimeReadsFallBackAndUseCallerCtx(t *testing.T) {
	a, _ := newTestAPI(t)
	ctx := context.Background()
	sess := &store.Session{Provider: "anthropic", Model: "m", Status: "active"}
	if err := a.Services.Store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := a.Services.Store.MarkSessionHalted(ctx, sess.ID, "halted for the test"); err != nil {
		t.Fatalf("MarkSessionHalted: %v", err)
	}
	fake := &failingRuntime{}
	a.Services.SessionRuntime = service.NewSessionRuntimeService(fake)

	details, err := a.sessionDetails(context.WithValue(ctx, runtimeCtxKey{}, true), sess.ID)
	if err != nil {
		t.Fatalf("sessionDetails: %v", err)
	}
	if details.Halt.IsHalted || details.Runtime.State != "none" {
		t.Fatalf("halt=%+v runtime=%+v, want the defaults", details.Halt, details.Runtime)
	}
	if fake.sawCallerCtx != 2 {
		t.Fatalf("%d of 2 reads ran under the caller's ctx", fake.sawCallerCtx)
	}
}
