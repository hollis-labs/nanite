package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// The *Before types rebuild the response structs as they were while their
// fields held store types, so the tests below can require the DTO-based
// structs to encode byte-for-byte the same.

type sessionDetailsResponseBefore struct {
	Session              *store.Session                           `json:"session"`
	PrimaryAgent         *store.AgentProfile                      `json:"primary_agent,omitempty"`
	DurableAttachments   []store.DurableAgentInstanceSessionState `json:"durable_attachments"`
	CurrentDurableAgent  *store.DurableAgentInstance              `json:"current_durable_agent,omitempty"`
	ActivityState        string                                   `json:"activity_state"`
	LastActivityAt       string                                   `json:"last_activity_at,omitempty"`
	LastUsefulActivityAt string                                   `json:"last_useful_activity_at,omitempty"`
	Halt                 sessionHaltDetail                        `json:"halt"`
	Usage                *store.SessionUsageSummary               `json:"usage,omitempty"`
	RecentDurableEvents  []store.DurableAgentEvent                `json:"recent_durable_events"`
	Runtime              sessionRuntimeDetail                     `json:"runtime"`
	BootSource           string                                   `json:"boot_source"`
	ImmutableStartFields []string                                 `json:"immutable_start_fields"`
	Checkpoint           checkpointDetail                         `json:"checkpoint"`
}

type harnessV1SessionResponseBefore struct {
	Session         *store.Session               `json:"session"`
	Details         sessionDetailsResponseBefore `json:"details"`
	StreamTransport string                       `json:"stream_transport"`
	RouteHints      harnessV1SessionRoutes       `json:"route_hints"`
}

type startSurfaceCapabilitiesBefore struct {
	SchemaVersion       int                          `json:"schema_version"`
	LifecycleClasses    []enumOption                 `json:"lifecycle_classes"`
	DurableStatuses     []enumOption                 `json:"durable_statuses"`
	LaunchSources       []enumOption                 `json:"launch_sources"`
	AttachmentRelations []enumOption                 `json:"attachment_relations"`
	RuntimeKinds        []runtimeKindOption          `json:"runtime_kinds"`
	RecipeKinds         []enumOption                 `json:"recipe_kinds"`
	WakeReasons         []enumOption                 `json:"wake_reasons"`
	SessionPolicies     []enumOption                 `json:"session_policies"`
	Recipes             []service.DurableAgentRecipe `json:"recipes"`
	DurableAgents       []store.DurableAgentInstance `json:"durable_agents"`
	Profiles            []store.AgentProfile         `json:"profiles"`
	Providers           []store.ProviderConfig       `json:"providers"`
	Models              []store.Model                `json:"models"`
	WorkRootHints       []workRootHint               `json:"work_root_hints"`
}

func TestAgentProfileDTOJSONKeys(t *testing.T) {
	p := populatedAgentProfile(t)
	got := mustJSON(t, agentProfileToDTO(&p))

	meta := map[string]bool{"manage_class": true, "editable": true, "copy_to_managed": true, "revision": true, "persisted": true}
	want := make([]string, 0, len(agentProfileViewKeys))
	for _, k := range agentProfileViewKeys {
		if !meta[k] {
			want = append(want, k)
		}
	}
	sort.Strings(want)
	if keys := sortedKeys(t, got); !reflect.DeepEqual(keys, want) {
		t.Fatalf("AgentProfileDTO JSON keys changed\n got: %v\nwant: %v", keys, want)
	}
	if want := mustJSON(t, p); string(got) != string(want) {
		t.Fatalf("profile DTO JSON differs from row JSON\n got: %s\nwant: %s", got, want)
	}
}

func consumersFixture(t *testing.T) (store.Session, store.AgentProfile, store.DurableAgentInstance, []store.DurableAgentInstanceSessionState, []store.DurableAgentEvent, *store.SessionUsageSummary) {
	t.Helper()
	var sess store.Session
	populate(t, &sess)
	agent := populatedAgentProfile(t)
	created := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	inst := store.DurableAgentInstance{ID: "inst-1", Name: "Curator", Slug: "curator", Status: "active", CreatedAt: created, UpdatedAt: created, ArchivedAt: &created, ProfileID: "profile", LifecycleClass: "harness", Provider: "anthropic", Model: "model", RuntimeKind: "api", LaunchSourceType: "api_chat", LaunchSourceID: "recipe", WorkRoot: "/work", CurrentSessionID: "session", URN: "msg://agent/test/curator", FailureReason: "failure", MetadataJSON: "{}"}
	states := []store.DurableAgentInstanceSessionState{{DurableAgentInstanceSession: store.DurableAgentInstanceSession{InstanceID: "inst-1", SessionID: "session", Relation: "primary", AttachedAt: created, DetachedAt: &created}, SessionStatus: "active", Provider: "anthropic", Model: "model", RuntimeState: "running", RuntimeFailureReason: "failure", HaltedAt: "stamp", HaltedReason: "budget"}}
	events := []store.DurableAgentEvent{{ID: "ev-1", InstanceID: "inst-1", EventType: "woke", Message: "hello", CreatedAt: created, StatusBefore: "starting", StatusAfter: "running", SessionID: "session", Source: "manual", MetadataJSON: "{}"}}
	usage := &store.SessionUsageSummary{MessageCount: 3, TotalTokens: 42, EstimatedCostUSD: 0.5}
	return sess, agent, inst, states, events, usage
}

func TestSessionDetailsResponseMatchesStoreEncoding(t *testing.T) {
	sess, agent, inst, states, events, usage := consumersFixture(t)
	runtime := sessionRuntimeDetail{State: "running", RuntimeID: "rt-1", PID: 7}
	halt := sessionHaltDetail{IsHalted: true, HaltedAt: "2026-09-30T00:00:00Z", HaltedReason: "budget"}
	immutable := []string{"provider", "model"}

	for name, withAgent := range map[string]bool{"with primary agent": true, "without primary agent": false} {
		before := sessionDetailsResponseBefore{
			Session: &sess, DurableAttachments: states, CurrentDurableAgent: &inst,
			ActivityState: "online", LastActivityAt: "a", LastUsefulActivityAt: "b", Halt: halt,
			Usage: usage, RecentDurableEvents: events, Runtime: runtime, BootSource: "api_default",
			ImmutableStartFields: immutable, Checkpoint: checkpointDetail{Status: "unknown"},
		}
		after := sessionDetailsResponse{
			Session: sessionToViewPtr(&sess), DurableAttachments: durableAgentInstanceSessionStateToViews(states), CurrentDurableAgent: durableAgentInstanceToView(&inst),
			ActivityState: "online", LastActivityAt: "a", LastUsefulActivityAt: "b", Halt: halt,
			Usage: sessionUsageToViewPtr(usage), RecentDurableEvents: durableAgentEventToViews(events), Runtime: runtime, BootSource: "api_default",
			ImmutableStartFields: immutable, Checkpoint: checkpointDetail{Status: "unknown"},
		}
		if withAgent {
			before.PrimaryAgent = &agent
			dto := agentProfileToDTO(&agent)
			after.PrimaryAgent = &dto
		}
		if got, want := mustJSON(t, after), mustJSON(t, before); string(got) != string(want) {
			t.Errorf("%s: session details JSON differs from the store-typed encoding\n got: %s\nwant: %s", name, got, want)
		}

		v1Before := harnessV1SessionResponseBefore{Session: before.Session, Details: before, StreamTransport: "sse", RouteHints: harnessV1SessionRoutesForSession(sess.ID)}
		v1After := harnessV1SessionResponse{Session: after.Session, Details: after, StreamTransport: "sse", RouteHints: harnessV1SessionRoutesForSession(sess.ID)}
		if got, want := mustJSON(t, v1After), mustJSON(t, v1Before); string(got) != string(want) {
			t.Errorf("%s: harness v1 session JSON differs from the store-typed encoding\n got: %s\nwant: %s", name, got, want)
		}
	}
}

func TestStartSurfaceCapabilitiesMatchesStoreEncoding(t *testing.T) {
	_, agent, inst, _, _, _ := consumersFixture(t)
	profiles := []store.AgentProfile{agent, {ID: "a2", Name: "Second", Slug: "second"}}
	durable := []store.DurableAgentInstance{inst}
	providers := []store.ProviderConfig{{ID: "p1", Name: "Anthropic", ProviderType: "anthropic"}}
	models := []store.Model{{ID: "m1", ModelID: "claude", DisplayName: "Claude", ProviderID: "p1"}}

	before := startSurfaceCapabilitiesBefore{
		SchemaVersion: 1, RuntimeKinds: runtimeKindOptions(), LifecycleClasses: lifecycleClassOptions(),
		DurableAgents: durable, Profiles: profiles, Providers: providers, Models: models,
		Recipes: []service.DurableAgentRecipe{},
	}
	after := startSurfaceCapabilities{
		SchemaVersion: 1, RuntimeKinds: runtimeKindOptions(), LifecycleClasses: lifecycleClassOptions(),
		DurableAgents: durableAgentInstanceToViews(durable), Profiles: agentProfilesToDTO(profiles), Providers: providerConfigsToView(providers), Models: modelsToView(models),
		Recipes: []service.DurableAgentRecipe{},
	}
	if got, want := mustJSON(t, after), mustJSON(t, before); string(got) != string(want) {
		t.Fatalf("start-surface JSON differs from the store-typed encoding\n got: %s\nwant: %s", got, want)
	}
	// The handler wraps profiles in nonNilSlice, so an empty list stays [].
	if got := string(mustJSON(t, nonNilSlice(agentProfilesToDTO(nil)))); got != "[]" {
		t.Fatalf("empty profiles = %s, want []", got)
	}
}

// TestHarnessV1RecoverSessionLookupErrorIs500 covers the other half of the
// recover route's errors.Is(sql.ErrNoRows) split: Sessions.Get wraps its
// error, and a lookup failure that is not a missing row must stay a 500
// rather than read as "session not found".
func TestHarnessV1RecoverSessionLookupErrorIs500(t *testing.T) {
	_, mux := newTestAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/harness/v1/sessions/any/recover", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("recover with a failing lookup = %d body=%s; want 500", w.Code, w.Body.String())
	}
}
