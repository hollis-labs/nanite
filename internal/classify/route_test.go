package classify

import "testing"

// TestClassifyRoute covers every route value plus the default fallback.
// Mirrors the table-driven shape of TestClassify (classify_test.go) so
// the rubric is auditable in one place.
func TestClassifyRoute(t *testing.T) {
	cases := []struct {
		name              string
		message           string
		wantRoute         Route
		wantTargetType    string
		wantSynthetic     bool
	}{
		// --- Default / chat-direct ---
		{
			name:      "empty input → chat-direct",
			message:   "",
			wantRoute: RouteChatDirect,
		},
		{
			name:      "ambiguous conversational → chat-direct",
			message:   "what do you think about this approach?",
			wantRoute: RouteChatDirect,
		},
		{
			name:      "tool-shaped chat without render verb → chat-direct",
			message:   "find me the file that defines the Classify function",
			wantRoute: RouteChatDirect,
		},

		// --- Rule 1 — explicit envelope-type cue ---
		{
			name:           "explicit type — report-card",
			message:        "show me a report-card with the latest sprint metrics",
			wantRoute:      RouteExecutorEnvelopeRender,
			wantTargetType: "report-card",
		},
		{
			name:           "explicit type — list-card without render verb still routes",
			message:        "I want a list-card for the open tickets",
			wantRoute:      RouteExecutorEnvelopeRender,
			wantTargetType: "list-card",
		},
		{
			name:           "explicit type — longest match wins",
			message:        "render a metric-card",
			wantRoute:      RouteExecutorEnvelopeRender,
			wantTargetType: "metric-card",
		},
		{
			name:           "explicit type with demo cue carries synthetic flag",
			message:        "give me an example report-card for testing",
			wantRoute:      RouteExecutorEnvelopeRender,
			wantTargetType: "report-card",
			wantSynthetic:  true,
		},

		// --- Rule 2 — render verb + demo cue (the c117 scenario) ---
		{
			name:          "render verb + demo cue → synthetic-allowed",
			message:       "let's do some testing — show me a card with sprint data",
			wantRoute:     RouteExecutorEnvelopeRender,
			wantSynthetic: true,
		},
		{
			name:          "demo + render verb (any order)",
			message:       "demo: render the open-tickets summary",
			wantRoute:     RouteExecutorEnvelopeRender,
			wantSynthetic: true,
		},
		{
			name:          "give me an example, render → synthetic",
			message:       "give me an example: render the project summary",
			wantRoute:     RouteExecutorEnvelopeRender,
			wantSynthetic: true,
		},

		// --- Rule 3 — render verb alone ---
		{
			name:      "render verb alone — show me",
			message:   "show me the project summary",
			wantRoute: RouteExecutorEnvelopeRender,
		},
		{
			name:      "render verb alone — render",
			message:   "render a card showing the open tickets",
			wantRoute: RouteExecutorEnvelopeRender,
		},
		{
			name:      "render verb alone — preview a",
			message:   "preview a card layout for the dashboard",
			wantRoute: RouteExecutorEnvelopeRender,
		},

		// --- Rule 4 — multi-step render keywords ---
		{
			name:      "multi-step — fetch the X",
			message:   "fetch the latest sprint metrics for me",
			wantRoute: RouteExecutorEnvelopeRender,
		},
		{
			name:      "multi-step — look up X",
			message:   "look up the open tickets and summarize",
			wantRoute: RouteExecutorEnvelopeRender,
		},

		// --- Edge cases ---
		{
			name:      "demo alone (no render verb) → chat-direct",
			message:   "let me test the build pipeline",
			wantRoute: RouteChatDirect,
		},
		{
			name:      "render-adjacent but no verb — chat-direct",
			message:   "tell me about the rendering pipeline",
			wantRoute: RouteChatDirect,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyRoute(IntentSignals{Message: c.message})
			if got.Route != c.wantRoute {
				t.Errorf("Route = %q, want %q (msg=%q)", got.Route, c.wantRoute, c.message)
			}
			if got.TargetEnvelopeType != c.wantTargetType {
				t.Errorf("TargetEnvelopeType = %q, want %q (msg=%q)",
					got.TargetEnvelopeType, c.wantTargetType, c.message)
			}
			if got.SyntheticAllowed != c.wantSynthetic {
				t.Errorf("SyntheticAllowed = %v, want %v (msg=%q)",
					got.SyntheticAllowed, c.wantSynthetic, c.message)
			}
		})
	}
}

// TestClassifyRoute_EnvelopeTypeProvider verifies the v1 allow-list
// gating: when the production provider returns an empty list (no
// passive-renderables reachable in this build), all envelope-render
// rules degrade to chat-direct.
func TestClassifyRoute_EnvelopeTypeProvider(t *testing.T) {
	original := envelopeTypeProvider
	t.Cleanup(func() { SetEnvelopeTypeProvider(original) })

	SetEnvelopeTypeProvider(func() []string { return nil })

	got := ClassifyRoute(IntentSignals{Message: "show me a report-card"})
	if got.Route != RouteChatDirect {
		t.Errorf("with empty type list: Route = %q, want %q", got.Route, RouteChatDirect)
	}
}

// TestClassifyRoute_CustomTypeProvider verifies a workspace that
// installs a narrower allow-list: only types in the installed list
// match the explicit-type rule.
func TestClassifyRoute_CustomTypeProvider(t *testing.T) {
	original := envelopeTypeProvider
	t.Cleanup(func() { SetEnvelopeTypeProvider(original) })

	SetEnvelopeTypeProvider(func() []string {
		return []string{"info-card"}
	})

	gotInfoCard := ClassifyRoute(IntentSignals{Message: "show me an info-card"})
	if gotInfoCard.Route != RouteExecutorEnvelopeRender {
		t.Errorf("info-card: Route = %q, want %q", gotInfoCard.Route, RouteExecutorEnvelopeRender)
	}
	if gotInfoCard.TargetEnvelopeType != "info-card" {
		t.Errorf("info-card: TargetEnvelopeType = %q, want info-card", gotInfoCard.TargetEnvelopeType)
	}

	// report-card not in the installed list — falls back to render-verb
	// rule (no explicit type pinned).
	gotReportCard := ClassifyRoute(IntentSignals{Message: "show me a report-card"})
	if gotReportCard.Route != RouteExecutorEnvelopeRender {
		t.Errorf("report-card: Route = %q, want %q", gotReportCard.Route, RouteExecutorEnvelopeRender)
	}
	if gotReportCard.TargetEnvelopeType != "" {
		t.Errorf("report-card: TargetEnvelopeType = %q, want empty (not in allow-list)",
			gotReportCard.TargetEnvelopeType)
	}
}

// TestRoute_IsValid covers the wire-string validity contract.
func TestRoute_IsValid(t *testing.T) {
	cases := []struct {
		r    Route
		want bool
	}{
		{RouteChatDirect, true},
		{RouteExecutorEnvelopeRender, true},
		{Route(""), false},
		{Route("nonsense"), false},
	}
	for _, c := range cases {
		if got := c.r.IsValid(); got != c.want {
			t.Errorf("Route(%q).IsValid() = %v, want %v", c.r, got, c.want)
		}
	}
}

// TestRoute_StringRoundTrip locks in the wire-string form.
func TestRoute_StringRoundTrip(t *testing.T) {
	if got := RouteChatDirect.String(); got != "chat_direct" {
		t.Errorf("RouteChatDirect.String() = %q, want %q", got, "chat_direct")
	}
	if got := RouteExecutorEnvelopeRender.String(); got != "executor_envelope_render" {
		t.Errorf("RouteExecutorEnvelopeRender.String() = %q, want %q",
			got, "executor_envelope_render")
	}
}

// TestV1EnvelopeTypeBaseline_NotEmpty guards against an accidental
// wipe of the baseline list. The baseline is the fallback when no
// production provider is wired (e.g. in a test that doesn't
// SetEnvelopeTypeProvider explicitly). An empty baseline would
// silently degrade every executor-render route to chat-direct.
func TestV1EnvelopeTypeBaseline_NotEmpty(t *testing.T) {
	if len(v1EnvelopeTypeBaseline) == 0 {
		t.Fatal("v1EnvelopeTypeBaseline is empty — would silently disable executor routing")
	}
}

// TestPhraseHitWordBoundary pins phraseHit's word-boundary semantics.
// Moved here from mode_test.go when mode.go was deleted (Phase 0 item 21
// — Cut Modes, in full) since route.go's anyPhraseHit is now the only
// consumer of phraseHit.
func TestPhraseHitWordBoundary(t *testing.T) {
	cases := []struct {
		haystack string
		phrase   string
		want     bool
	}{
		{"plan a launch", "plan a", true},
		{"i plan a launch", "plan a", true},
		{"planet a", "plan a", false}, // 'plan' followed by 'e' — not a boundary
		{"planning", "plan", false},   // 'plan' followed by 'n'
		{"the plan, then", "plan", true},
		{"unplanned", "plan", false}, // 'plan' preceded by 'n'
		{"", "plan", false},
		{"plan", "plan", true},
	}
	for _, tc := range cases {
		got := phraseHit(tc.haystack, tc.phrase)
		if got != tc.want {
			t.Errorf("phraseHit(%q, %q) = %v, want %v", tc.haystack, tc.phrase, got, tc.want)
		}
	}
}
