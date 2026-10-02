package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/hollis-labs/go-envelopes/admin"
	"github.com/hollis-labs/go-envelopes/admin/adminhttp"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
)

const adminReadOnlyReason = "This admin adapter is read-only; use the existing preferences UI."

func adminUnavailable() error {
	return &admin.Failure{Code: admin.BackendUnavailable, Message: "The admin backend is unavailable."}
}
func adminReadOnly() error {
	return &admin.Failure{Code: admin.Forbidden, Message: "This admin adapter is read-only."}
}

// NewAdminHandler declares a read-only application adapter. authenticate must
// require both configured operator credentials; caller identity headers are not
// authentication. Discovery does not read settings or probe process activity.
func (a *API) NewAdminHandler(authenticate func(*http.Request) (string, bool)) (http.Handler, error) {
	revision := "nanite.admin.v1.preferences"
	if a.adminTracker() != nil {
		revision += ".processes"
	}
	streamEnum := []admin.Scalar{}
	for _, value := range service.ToolStreamBehaviorValues() {
		scalar, err := admin.String(value)
		if err != nil {
			return nil, err
		}
		streamEnum = append(streamEnum, scalar)
	}
	retentionEnum := []admin.Scalar{}
	for _, value := range service.ToolDrawerRetentionValues() {
		scalar, err := admin.Integer(int64(value))
		if err != nil {
			return nil, err
		}
		retentionEnum = append(retentionEnum, scalar)
	}
	definition := admin.Definition{
		App: admin.App{ID: "nanite", Label: "Nanite"}, Revision: revision, BasePath: "/api",
		Groups: []admin.Group{{ID: "preferences", Label: "Preferences", Scope: admin.Scope{Kind: "app", ID: "nanite"},
			Fields: []admin.Field{
				{Key: "tool_stream_behavior", Type: admin.StringType, Title: "Tool stream behavior", Required: true, Enum: streamEnum, ReadOnlyReason: adminReadOnlyReason},
				{Key: "tool_drawer_retention", Type: admin.IntegerType, Title: "Tool drawer retention", Required: true, Enum: retentionEnum, ReadOnlyReason: adminReadOnlyReason},
			}, Resolution: admin.Resolution{Precedence: []admin.SourceKind{admin.OverrideSource}},
			Capabilities: admin.Capabilities{CanRead: true}, Backend: adminPreferencesBackend{api: a, revision: revision},
		}},
	}
	if a.adminTracker() != nil {
		definition.Health = []admin.HealthResource{{ID: "cli-process-activity", Label: "Tracked CLI output inactivity (five-minute heuristic)", PollIntervalMS: 5000, StaleAfterMS: 15000, Read: func(ctx context.Context) (admin.HealthObservation, error) {
			if err := ctx.Err(); err != nil {
				return admin.HealthObservation{}, err
			}
			tracker := a.adminTracker()
			if tracker == nil {
				return admin.HealthObservation{}, adminUnavailable()
			}
			return adminProcessActivity(tracker.HealthCheck(5 * time.Minute)), nil
		}}}
		definition.Stats = []admin.StatResource{{ID: "cli-process-count", Label: "Tracked CLI processes", PollIntervalMS: 5000, StaleAfterMS: 15000, Unit: admin.Count, Kind: admin.Gauge, Read: func(ctx context.Context) (admin.StatObservation, error) {
			if err := ctx.Err(); err != nil {
				return admin.StatObservation{}, err
			}
			tracker := a.adminTracker()
			if tracker == nil {
				return admin.StatObservation{}, adminUnavailable()
			}
			count := float64(tracker.Count())
			return admin.StatObservation{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Value: &count}, nil
		}}}
	}
	registry, err := admin.New(definition)
	if err != nil {
		return nil, err
	}
	authorize := func(r *http.Request, _ adminhttp.Resource) error {
		if authenticate != nil {
			if actor, ok := authenticate(r); ok && actor != "" {
				return nil
			}
		}
		return &admin.Failure{Code: admin.Unauthenticated, Message: "Authentication is required."}
	}
	handler, err := adminhttp.NewHandler(adminhttp.Config{BasePath: "/api", Authorize: authorize,
		Resolve:        func(*http.Request) (*admin.Registry, error) { return registry, nil },
		ProtectCommand: func(*http.Request) error { return adminReadOnly() },
	})
	if err != nil {
		return nil, err
	}
	// Host policy denies every POST, including unknown routes, before body reads.
	// It also preserves the Basic challenge independently of optional global auth.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authErr := authorize(r, adminhttp.Resource{}); authErr != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="nanite"`)
			writeAdminPolicyFailure(w, http.StatusUnauthorized, admin.Unauthenticated, "Authentication is required.")
			return
		}
		if r.Method == http.MethodPost {
			writeAdminPolicyFailure(w, http.StatusForbidden, admin.Forbidden, "This admin adapter is read-only.")
			return
		}
		handler.ServeHTTP(w, r)
	}), nil
}

func writeAdminPolicyFailure(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(admin.ErrorResponse{Error: admin.Failure{Code: code, Message: message}}); err != nil {
		slog.Debug("api: write admin policy response failed", "err", err)
	}
}

func (a *API) adminTracker() *chat.ProcessTracker {
	if a == nil || a.Services == nil {
		return nil
	}
	return a.Services.ProcessTracker
}

// adminProcessActivity aggregates the existing output-inactivity heuristic;
// healthy does not assert OS liveness or whole-application health. Identifiers,
// PIDs and timings are deliberately excluded from the public observation.
func adminProcessActivity(processes []chat.ProcessHealth) admin.HealthObservation {
	status := admin.Healthy
	message := "No tracked CLI process exceeds five minutes without output."
	for _, process := range processes {
		if process.IsStale {
			status = admin.Degraded
			message = "At least one tracked CLI process exceeds five minutes without output."
			break
		}
	}
	return admin.HealthObservation{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: status,
		Checks: []admin.HealthCheck{{ID: "output-inactivity", Label: "Tracked output inactivity", Status: status, Message: message}},
	}
}

type adminPreferencesBackend struct {
	api      *API
	revision string
}

func (b adminPreferencesBackend) Read(ctx context.Context) (admin.State, error) {
	if b.api == nil || b.api.Services == nil || b.api.Services.Settings == nil {
		return admin.State{}, adminUnavailable()
	}
	preferences, err := b.api.Services.Settings.AdminPreferences(ctx)
	if err != nil {
		return admin.State{}, err
	}
	if preferences == nil {
		return admin.State{}, adminUnavailable()
	}
	stream, err := admin.String(preferences.ToolStreamBehavior)
	if err != nil {
		return admin.State{}, err
	}
	retention, err := admin.Integer(int64(preferences.ToolDrawerRetention))
	if err != nil {
		return admin.State{}, err
	}
	value := func(s admin.Scalar) admin.ResolvedValue {
		return admin.ResolvedValue{Present: true, Value: s,
			Source: &admin.Source{Kind: admin.OverrideSource, Label: "Persisted application preference"}, HasOverride: true, ReadOnlyReason: adminReadOnlyReason, ApplyState: admin.UnknownApply}
	}
	return admin.State{Revision: b.revision, Version: preferences.Version, Values: map[string]admin.ResolvedValue{
		"tool_stream_behavior": value(stream), "tool_drawer_retention": value(retention),
	}}, nil
}
func (adminPreferencesBackend) Preview(context.Context, admin.Changes) (admin.State, error) {
	return admin.State{}, &admin.Failure{Code: admin.Unsupported, Message: "This admin adapter is read-only."}
}
func (adminPreferencesBackend) WithTransaction(context.Context, func(admin.GroupTransaction) error) error {
	return &admin.Failure{Code: admin.Unsupported, Message: "This admin adapter is read-only."}
}
