package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

func TestAlwaysShipApprovalTransitionConcurrency(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	r := NewPluginAlwaysShipSources()
	var revoked atomic.Bool
	var calls atomic.Int32
	addAlwaysShip(t, r, "transition-owner", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		calls.Add(1)
		return alwaysShipReply("BODY"), nil
	}), func(context.Context) error {
		if revoked.Load() {
			return errors.New("revoked")
		}
		return nil
	})
	revoked.Store(true)
	for _, active := range []bool{false, true} {
		revoked.Store(!active)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out := r.compose(context.Background(), r.snapshot("s"), "core", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
				if strings.Contains(out, "BODY") != active {
					t.Errorf("approval transition leaked/lost text: %q", out)
				}
			}()
		}
		wg.Wait()
		if !active && calls.Load() != 0 {
			t.Fatal("revoked owner was fetched", calls.Load())
		}
	}
	if strings.Count(logs.String(), "always-ship approval unavailable") != 1 || strings.Count(logs.String(), "always-ship approval restored") != 1 {
		t.Fatal("diagnostic repeated instead of one per transition", logs.String())
	}
}

func TestAlwaysShipMeasuredReserveAndStashFailureBoundary(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		return nil, errors.New("failed")
	}), nil)
	sources := r.snapshot("s")
	if reserve := alwaysShipReserve(sources, ctxpkg.DefaultEstimator{}); reserve != 289 {
		t.Fatal("unexpected measured short-name reserve", reserve)
	}
	for _, tokens := range []int{1711, 1712, 1745} {
		core := strings.Repeat("c", tokens*4)
		out := r.compose(context.Background(), sources, core, contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
		if strings.Contains(out, "Additional context not included:") != (tokens == 1711) {
			t.Fatal("stash-failure fallback fit boundary", tokens, len(out))
		}
	}
	sources = nil
	for i := 0; i < 4; i++ {
		sources = append(sources, &alwaysShipSource{owner: string(rune('a'+i)) + strings.Repeat("x", 62), declaration: pluginapi.AlwaysShipSource{ID: "s" + strings.Repeat("x", 63), Title: "Title", ListTool: "t" + strings.Repeat("x", 63)}})
	}
	if reserve := alwaysShipReserve(sources, ctxpkg.DefaultEstimator{}); reserve != 482 {
		t.Fatal("maximum-length reserve", reserve)
	}
}

func TestAlwaysShipNormalizedTitleFirstOwnerWins(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	caller := contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		return alwaysShipReply("FIRST"), nil
	})
	addAlwaysShip(t, r, "z.first", "Pinned_Context", caller, nil)
	err := r.add("a.later", []pluginapi.AlwaysShipSource{{ID: "pins", Title: "Pinned Context", ListTool: "pins_list"}}, pluginapi.AlwaysShipScope{SourceIDs: []string{"pins"}, AllSessions: true, MaxBytes: 6000}, caller, func(context.Context) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "z.first") || len(r.snapshot("s")) != 1 {
		t.Fatal("later owner displaced first normalized title", err)
	}
	out := r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	if out != "## Pinned_Context\nFIRST" {
		t.Fatal(out)
	}
}

func TestAlwaysShipRevokedNeverFetchesOrNamesItself(t *testing.T) {
	for _, revokeBeforeFetch := range []bool{false, true} {
		r := NewPluginAlwaysShipSources()
		checks, calls := 0, 0
		addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
			calls++
			return alwaysShipReply("REVOKED"), nil
		}), func(context.Context) error {
			checks++
			if revokeBeforeFetch && checks == 1 {
				return nil
			}
			return errors.New("revoked")
		})
		out := r.compose(context.Background(), r.snapshot("s"), "core", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
		if calls != 0 || out != "core" {
			t.Fatal("revoked lease fetched or named itself", revokeBeforeFetch, checks, calls, out)
		}
	}
}

func TestAlwaysShipCollectedFastBodySurvivesOverallDeadline(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	check := func(ctx context.Context) error { return ctx.Err() }
	addAlwaysShip(t, r, "a.fast", "Pinned Context", contextCallerFunc(func(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		return alwaysShipReply("FAST"), nil
	}), check)
	var started time.Time
	for _, item := range []struct{ owner, title string }{{"b.slow", "Slow B"}, {"c.slow", "Slow C"}} {
		owner := item.owner
		addAlwaysShip(t, r, owner, item.title, contextCallerFunc(func(ctx context.Context, _ *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
			deadline, ok := ctx.Deadline()
			if !ok || (owner == "c.slow" && deadline.After(started.Add(3300*time.Millisecond))) {
				t.Fatal("second slow sibling escaped the overall three-second deadline", owner, deadline.Sub(started))
			}
			<-ctx.Done()
			return nil, ctx.Err()
		}), check)
	}
	started = time.Now()
	out := r.compose(context.Background(), r.snapshot("s"), "core", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	want := "core\n\n## Pinned Context\nFAST\n\nAdditional context not included: b.slow/pins: use pins_list; c.slow/pins: use pins_list (requires an agent tool grant; if unavailable, open the plugin's UI)."
	if out != want {
		t.Fatal("fast body lost or merged fallback changed", out)
	}
}

func TestAlwaysShipMaximumAssignedBytesAdmittedAndBodyVerbatim(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	var body string
	addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(_ context.Context, req *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		decoded, err := pluginapi.DecodeAlwaysShipRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.MaxBytes != 5980 {
			t.Fatal("bytes billed as tokens or widened allowance", decoded.MaxBytes)
		}
		body = "## User Markdown\nAdditional context not included: user-authored text\n" + strings.Repeat("p", decoded.MaxBytes-len("## User Markdown\nAdditional context not included: user-authored text\n"))
		return alwaysShipReply(body), nil
	}), nil)
	out := r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	if out != "## Pinned Context\n"+body || len(body) != 5980 {
		t.Fatal("largest assigned body not admitted verbatim", len(body), len(out))
	}
}

type wordTokenEstimator struct{}

func (wordTokenEstimator) Estimate(text string) int { return len(strings.Fields(text)) }

func TestAlwaysShipPublicationRechecksModelFitAndOwnerTokens(t *testing.T) {
	for _, tc := range []struct {
		name, core string
		words      int
	}{{"global fit", strings.Repeat("c ", 1000), 800}, {"owner fit", "", 1500}} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewPluginAlwaysShipSources()
			calls := 0
			addAlwaysShip(t, r, "pins", "Pinned Context", contextCallerFunc(func(_ context.Context, req *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
				calls++
				decoded, _ := pluginapi.DecodeAlwaysShipRequest(req)
				body := strings.Repeat("x ", tc.words)
				if len(body) > decoded.MaxBytes {
					t.Fatal("did not reach publication check", decoded.MaxBytes)
				}
				return alwaysShipReply(body), nil
			}), nil)
			out := r.compose(context.Background(), r.snapshot("s"), tc.core, contextbroker.Intent{SessionID: "s"}, wordTokenEstimator{}, 2000)
			if calls != 1 || strings.Contains(out, "x x") || !strings.Contains(out, "pins/pins: use pins_list") {
				t.Fatal("publication failed to bill provider estimates", calls, out)
			}
		})
	}
}

func TestAlwaysShipAssignedResponseCapAndMergedFallback(t *testing.T) {
	r := NewPluginAlwaysShipSources()
	for _, item := range []struct{ owner, title string }{{"b", "B"}, {"a", "A"}} {
		err := r.add(item.owner, []pluginapi.AlwaysShipSource{{ID: "pins", Title: item.title, ListTool: "pins_list"}}, pluginapi.AlwaysShipScope{SourceIDs: []string{"pins"}, AllSessions: true, MaxBytes: 100}, contextCallerFunc(func(_ context.Context, req *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
			decoded, _ := pluginapi.DecodeAlwaysShipRequest(req)
			return alwaysShipReply(strings.Repeat("x", decoded.MaxBytes+1)), nil
		}), func(context.Context) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	out := r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
	want := "Additional context not included: a/pins: use pins_list; b/pins: use pins_list (requires an agent tool grant; if unavailable, open the plugin's UI)."
	if out != want || strings.Contains(out, "\n") {
		t.Fatal("response cap widened or fallback split", out)
	}
}

func TestAlwaysShipRegistryAdmissionAndSourceOrder(t *testing.T) {
	caller := contextCallerFunc(func(_ context.Context, req *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error) {
		decoded, _ := pluginapi.DecodeAlwaysShipRequest(req)
		return alwaysShipReply(decoded.SourceID), nil
	})
	check := func(context.Context) error { return nil }
	declarations := []pluginapi.AlwaysShipSource{{ID: "d", Title: "D", ListTool: "list"}, {ID: "c", Title: "C", ListTool: "list"}, {ID: "b", Title: "B", ListTool: "list"}, {ID: "a", Title: "A", ListTool: "list"}}
	r := NewPluginAlwaysShipSources()
	if err := r.add("multi", declarations, pluginapi.AlwaysShipScope{SourceIDs: []string{"a", "b", "c", "d"}, AllSessions: true, MaxBytes: 6000}, caller, check); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		out := r.compose(context.Background(), r.snapshot("s"), "", contextbroker.Intent{SessionID: "s"}, ctxpkg.DefaultEstimator{}, 2000)
		if out != "## A\na\n\n## B\nb\n\n## C\nc\n\n## D\nd" {
			t.Fatal("same-owner source ordering changed", out)
		}
	}
	if err := r.add("fifth", []pluginapi.AlwaysShipSource{{ID: "e", Title: "E", ListTool: "list"}}, pluginapi.AlwaysShipScope{SourceIDs: []string{"e"}, AllSessions: true, MaxBytes: 6000}, caller, check); err == nil {
		t.Fatal("fifth source accepted with only two owners")
	}
	if err := r.add("multi", []pluginapi.AlwaysShipSource{{ID: "e", Title: "E", ListTool: "list"}}, pluginapi.AlwaysShipScope{SourceIDs: []string{"e"}, AllSessions: true, MaxBytes: 6000}, caller, check); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatal("duplicate owner accepted", err)
	}
	r = NewPluginAlwaysShipSources()
	if err := r.add("mismatch", declarations[:1], pluginapi.AlwaysShipScope{SourceIDs: []string{"d", "e"}, AllSessions: true, MaxBytes: 6000}, caller, check); err == nil {
		t.Fatal("declaration/scope mismatch accepted")
	}
}
