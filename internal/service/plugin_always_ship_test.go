package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	ctxpkg "github.com/hollis-labs/substrate/agent/contextwindow"
)

func alwaysShipReply(body string) *sdkprocess.HTTPResponse {
	raw, _ := json.Marshal(pluginapi.AlwaysShipResponse{Protocol: 1, Body: body})
	return &sdkprocess.HTTPResponse{Status: 200, Body: raw}
}
func addAlwaysShip(t *testing.T, r *PluginAlwaysShipSources, owner, title string, caller contextHTTPCaller, check func(context.Context) error) {
	t.Helper()
	if check == nil {
		check = func(context.Context) error { return nil }
	}
	if checkErr := r.add(owner, []pluginapi.AlwaysShipSource{{ID: "pins", Title: title, ListTool: "pins_list"}}, pluginapi.AlwaysShipScope{SourceIDs: []string{"pins"}, SessionIDs: []string{"s"}, MaxBytes: 6000}, caller, check); checkErr != nil {
		t.Fatal(checkErr)
	}
}
func TestAlwaysShipBoundsOrderingAndFallback(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	var order []string
	for _, entry := range []struct{ owner, title string }{{"a.notes", "Notes"}, {"z.pins", "Pinned Context"}, {"b.notes", "Other"}, {"c.notes", "Last"}} {
		owner := entry.owner
		addAlwaysShip(t, r, owner, entry.title, contextCallerFunc(func(_ context.Context, request *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
			order = append(order, owner)
			decoded, requestErr := pluginapi.DecodeAlwaysShipRequest(request)
			if requestErr != nil || decoded.SourceID != "pins" || decoded.AgentID != "a" || decoded.Intent != "review_session" || decoded.MaxBytes > 6000 || request.Path != "/__nanite/context/always-ship/fetch" {
				t.Fatal(decoded, requestErr)
			}
			if strings.Contains(string(request.Body), "query") {
				t.Fatal("shared query text")
			}
			if owner == "b.notes" {
				return nil, errors.New("failed")
			}
			return alwaysShipReply("[pinned] " + owner), nil
		}), nil)
	}
	if err := r.add("fifth", []pluginapi.AlwaysShipSource{{ID: "pins", Title: "Fifth", ListTool: "pins_list"}}, pluginapi.AlwaysShipScope{SourceIDs: []string{"pins"}, AllSessions: true, MaxBytes: 10}, contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) { return nil, nil }), func(context.Context) error { return nil }); err == nil {
		t.Fatal("fifth owner accepted")
	}
	sources := r.snapshot("s")
	out := r.compose(context.Background(), sources, "core", contextbroker.Intent{SessionID: "s", AgentID: "a", Type: "review_session", QueryText: "secret"}, ctxpkg.DefaultEstimator{}, 2000)
	if strings.Join(order, ",") != "z.pins,a.notes,b.notes,c.notes" || !strings.HasPrefix(out, "core\n\n## Pinned Context\n[pinned] z.pins") || !strings.Contains(out, "Additional context not included: b.notes/pins: use pins_list") {
		t.Fatal(order, out)
	}
	if strings.Count(out, "Additional context not included:") != 1 || contextbroker.EstimateTokens(out) > 1744 {
		t.Fatal("fallback or budget", out)
	}
	if len(r.snapshot("elsewhere")) != 0 {
		t.Fatal("scope leaked")
	}
}

func TestAlwaysShipReserveAndOwnerCap(t *testing.T) {
	for _, coreTokens := range []int{0, 1480, 1700, 1744, 1872, 2000} {
		t.Run(fmt.Sprint(coreTokens), func(t *testing.T) {
			r := NewPluginAlwaysShipSources()
			addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(_ context.Context, req *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				decoded, requestErr := pluginapi.DecodeAlwaysShipRequest(req)
				if requestErr != nil {
					t.Fatal(requestErr)
				}
				return alwaysShipReply(strings.Repeat("p", decoded.MaxBytes)), nil
			}), nil)
			sources := r.snapshot("s")
			core := strings.Repeat("c", coreTokens*4)
			reserve := alwaysShipReserve(sources, ctxpkg.DefaultEstimator{})
			if reserve <= 256 {
				t.Fatal("fallback not reserved")
			}
			out := r.compose(context.Background(), sources, core, contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
			if coreTokens+reserve > 2000 {
				if out != core {
					t.Fatal("inline stash failure changed")
				}
				return
			}
			if contextbroker.EstimateTokens(out) > 1744 {
				t.Fatal("late reserve consumed")
			}
			pluginText := strings.TrimPrefix(out, core)
			if core == "" && contextbroker.EstimateTokens(pluginText) > 1500 {
				t.Fatal("owner cap exceeded")
			}
			window := ctxpkg.NewContextWindow(200000, nil)
			window.SetContent(ctxpkg.SlotUserContext, out)
			window.Assemble()
			if window.Slot(ctxpkg.SlotUserContext).Content != out {
				t.Fatal("window truncated composed output")
			}
		})
	}
	// Oversized wire/body is failure, not partial success or truncation.
	r := NewPluginAlwaysShipSources()
	addAlwaysShip(t, r, "oversize", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		return alwaysShipReply(strings.Repeat("p", 6001)), nil
	}), nil)
	if out := r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000); !strings.HasPrefix(out, "Additional context not included:") || strings.Contains(out, "pppp") {
		t.Fatal(out)
	}
}

func TestAlwaysShipRevocationUnloadAndReload(t *testing.T) {
	for _, mode := range []string{"revoked", "unloaded", "reloaded"} {
		t.Run(mode, func(t *testing.T) {
			r := NewPluginAlwaysShipSources()
			var revoked atomic.Bool
			check := func(context.Context) error {
				if revoked.Load() {
					return errors.New("revoked")
				}
				return nil
			}
			addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				if mode == "revoked" {
					revoked.Store(true)
				} else {
					r.RemovePluginAlwaysShipSources("pins")
				}
				if mode == "reloaded" {
					addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
						return alwaysShipReply("NEW"), nil
					}), nil)
				}
				return alwaysShipReply("STALE"), nil
			}), check)
			out := r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
			if strings.Contains(out, "STALE") || (mode == "revoked" && out != "") || (mode != "revoked" && !strings.Contains(out, "pins/pins: use pins_list")) {
				t.Fatal("stale lifetime published", out)
			}
			if mode == "unloaded" && len(r.snapshot("s")) != 0 {
				t.Fatal("unloaded lease remained")
			}
		})
	}
}
func TestAlwaysShipSlowOwnerAndCancellation(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	addAlwaysShip(t, r, "slow", "Pinned Context", contextCallerFunc(func(ctx context.Context, _ *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	out := r.compose(ctx, r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	if !strings.Contains(out, "slow/pins: use pins_list") {
		t.Fatal(out)
	}
	// Unload cancels the owned in-flight call before child shutdown.
	started := make(chan struct{})
	r.RemovePluginAlwaysShipSources("slow")
	addAlwaysShip(t, r, "slow", "Pinned Context", contextCallerFunc(func(ctx context.Context, _ *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}), nil)
	done := make(chan string, 1)
	go func() {
		done <- r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	}()
	<-started
	r.RemovePluginAlwaysShipSources("slow")
	select {
	case out := <-done:
		if !strings.Contains(out, "slow/pins") {
			t.Fatal(out)
		}
	case <-time.After(time.Second):
		t.Fatal("unload did not cancel fetch")
	}
}

func TestAlwaysShipMultipleSourcesShareOwnerAuthority(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	var total int
	caller := contextCallerFunc(func(_ context.Context, req *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		decoded, requestErr := pluginapi.DecodeAlwaysShipRequest(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body := strings.Repeat("x", min(decoded.MaxBytes, 60))
		total += len(body)
		return alwaysShipReply(body), nil
	})
	scope := pluginapi.AlwaysShipScope{SourceIDs: []string{"a", "b"}, AllSessions: true, MaxBytes: 100}
	if checkErr := r.add("multi", []pluginapi.AlwaysShipSource{{ID: "a", Title: "A", ListTool: "list"}, {ID: "b", Title: "B", ListTool: "list"}}, scope, caller, func(context.Context) error { return nil }); checkErr != nil {
		t.Fatal(checkErr)
	}
	// Caller mutations cannot widen the accepted session/source snapshot.
	scope.SourceIDs[0] = "other"
	out := r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	if total != 100 || !strings.Contains(out, "## A\n") || !strings.Contains(out, "## B\n") {
		t.Fatal(total, out)
	}
}

func TestAlwaysShipAssembleSlotsIntentExemptions(t *testing.T) {
	for _, tc := range []struct{ message, intent string }{{"review history", "review_session"}, {"recall decision", "recall_decision"}, {"resume work", "resume_task"}} {
		t.Run(tc.intent, func(t *testing.T) {
			db, err := storetest.New(t, context.Background(), t.TempDir()+"/db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.DB.Close() })
			session := &store.Session{ID: "s"}
			if checkErr := db.CreateSession(context.Background(), session); checkErr != nil {
				t.Fatal(checkErr)
			}
			if checkErr := db.CreateMessage(context.Background(), &store.Message{ID: "m", SessionID: "s", Role: "user", Content: tc.message}); checkErr != nil {
				t.Fatal(checkErr)
			}
			registry := NewPluginAlwaysShipSources()
			addAlwaysShip(t, registry, "pins", "Pinned Context", contextCallerFunc(func(_ context.Context, req *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				decoded, requestErr := pluginapi.DecodeAlwaysShipRequest(req)
				if requestErr != nil || decoded.Intent != tc.intent || decoded.AgentID != "a" {
					t.Fatal(decoded, requestErr)
				}
				return alwaysShipReply("[pinned] EXACT\r\nbytes"), nil
			}), nil)
			service := NewContextService(ContextServiceConfig{Client: chat.NewContextClient(db), AlwaysShip: registry})
			result, err := service.AssembleSlots(context.Background(), session, &store.AgentProfile{ID: "a", Slug: "test"}, nil, "", 200000, "")
			if err != nil {
				t.Fatal(err)
			}
			if !result.AlwaysShipActive {
				t.Fatal("active owner did not select conditional late delivery")
			}
			wanted := "## Pinned Context\n[pinned] EXACT\r\nbytes"
			if composeUserPayload(result, "USER") != wanted+"\n\nUSER" {
				t.Fatal("CLI payload missed persistent context")
			}
			if result.Window.Slot(ctxpkg.SlotUserContext).Content != wanted || !strings.Contains(result.SystemPrompt, wanted) {
				t.Fatal("composed bytes absent", result.SystemPrompt)
			}
			found := false
			for _, block := range result.Blocks {
				if block.SlotName == ctxpkg.SlotUserContext {
					found = true
					if block.Content != wanted {
						t.Fatal("wire or cache marker changed", block)
					}
				}
			}
			if !found {
				t.Fatal("no final UserContext block")
			}
			capture := inspector.NewService()
			(&chatServiceImpl{inspector: capture}).recordInspectorSlots("s", "turn", result)
			// Query independently enforces session permission; inspect the recorded
			// turn directly here to pin the producer's final composed bytes.
			captured := false
			for _, slot := range capture.Snapshot("s", "turn").Slots {
				if slot.Name == ctxpkg.SlotUserContext {
					captured = slot.Content == wanted
				}
			}
			if !captured {
				t.Fatal("context_slots producer missed final content")
			}
		})
	}
}

func TestAlwaysShipCoreDecisionPrecedesComposition(t *testing.T) {
	for _, stashWorks := range []bool{true, false} {
		t.Run(fmt.Sprint(stashWorks), func(t *testing.T) {
			db, err := storetest.New(t, context.Background(), t.TempDir()+"/db")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.DB.Close() })
			session := &store.Session{ID: "s"}
			if checkErr := db.CreateSession(context.Background(), session); checkErr != nil {
				t.Fatal(checkErr)
			}
			prompt := strings.Repeat("c", 7200)
			if checkErr := db.SetSessionContextPrompt(context.Background(), "s", prompt); checkErr != nil {
				t.Fatal(checkErr)
			}
			registry := NewPluginAlwaysShipSources()
			calls := 0
			addAlwaysShip(t, registry, "pins", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				calls++
				return alwaysShipReply("[pinned] hello"), nil
			}), nil)
			var stasher contextbroker.SlotStasher
			if stashWorks {
				stasher = fakeArtifactStasher{}
			}
			svc := NewContextService(ContextServiceConfig{Client: chat.NewContextClient(db), AlwaysShip: registry, SlotStasher: stasher})
			result, err := svc.AssembleSlots(context.Background(), session, &store.AgentProfile{ID: "a"}, nil, "", 200000, "")
			if err != nil {
				t.Fatal(err)
			}
			var decision contextbroker.SlotDecision
			for _, d := range result.Plan.Decisions {
				if d.SlotName == ctxpkg.SlotUserContext {
					decision = d
				}
			}
			if stashWorks {
				if decision.Action != contextbroker.ActionPointer || !strings.HasPrefix(decision.Content, "<ref:artifact_id=") || !strings.Contains(decision.Content, "## Pinned Context\n[pinned] hello") || calls != 1 {
					t.Fatal("plugin did not follow core pointer", decision, calls)
				}
				if result.Window.Slot(ctxpkg.SlotUserContext).Content != decision.Content {
					t.Fatal("wire differs from composition")
				}
			} else {
				if decision.Action != contextbroker.ActionShip || decision.ReasonTag != "pointer_fallback_ship" || decision.Content != "## Session Context\n"+prompt || calls != 0 {
					t.Fatal("stash failure changed core inline behavior", decision.ReasonTag, calls)
				}
			}
			// No active owner leaves the historic 2000-token core threshold intact.
			registry.RemovePluginAlwaysShipSources("pins")
			result, err = svc.AssembleSlots(context.Background(), session, &store.AgentProfile{ID: "a"}, nil, "", 200000, "")
			if err != nil {
				t.Fatal(err)
			}
			if result.AlwaysShipActive || result.Window.Slot(ctxpkg.SlotUserContext).Content != "## Session Context\n"+prompt {
				t.Fatal("inactive registry reduced core threshold")
			}
		})
	}
}

func TestAlwaysShipReservedDocumentsRefusedAtRuntime(t *testing.T) {
	registry := NewPluginAlwaysShipSources()
	calls := 0
	caller := contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		calls++
		return alwaysShipReply("docs"), nil
	})
	if err := registry.add("docs", []pluginapi.AlwaysShipSource{{ID: "docs", Title: "Session_Documents", ListTool: "documents_list"}}, pluginapi.AlwaysShipScope{SourceIDs: []string{"docs"}, AllSessions: true, MaxBytes: 6000}, caller, func(context.Context) error { return nil }); err == nil {
		t.Fatal("reserved title installed")
	}
	addAlwaysShip(t, registry, "pins", "Pinned Context", caller, nil)
	sources := registry.snapshot("s")
	sources[0].declaration.Title = "SESSION.DOCUMENTS"
	out := registry.compose(context.Background(), sources, "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	if calls != 0 || !strings.Contains(out, "Additional context not included:") {
		t.Fatal("reserved runtime title fetched", calls, out)
	}
}

func TestAlwaysShipEmptySuccessVersusFetchFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reply    *sdkprocess.HTTPResponse
		fallback bool
	}{
		{"empty", alwaysShipReply(""), false},
		{"nil", nil, true},
		{"failed status", &sdkprocess.HTTPResponse{Status: 500, Body: []byte(`{"protocol":1,"body":"DO NOT SHIP"}`)}, true},
		{"malformed", &sdkprocess.HTTPResponse{Status: 200, Body: []byte(`{"protocol":1}`)}, true},
		{"wrong protocol", &sdkprocess.HTTPResponse{Status: 200, Body: []byte(`{"protocol":2,"body":"DO NOT SHIP"}`)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewPluginAlwaysShipSources()
			addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(ctx context.Context, _ *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Fatal("missing child deadline")
				}
				return tc.reply, nil
			}), nil)
			out := r.compose(context.Background(), r.snapshot("s"), "core", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
			if strings.Contains(out, "Additional context not included:") != tc.fallback || strings.Contains(out, "DO NOT SHIP") {
				t.Fatal("failed fetch became success", out)
			}
			if !tc.fallback && out != "core" {
				t.Fatal("empty success gained heading", out)
			}
		})
	}
}
