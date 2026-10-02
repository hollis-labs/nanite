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

func adminUnavailable() error {
	return &admin.Failure{Code: admin.BackendUnavailable, Message: "The admin backend is unavailable."}
}

// NewAdminHandler declares the two-preference application adapter. authenticate must
// require both configured operator credentials; caller identity headers are not
// authentication. Discovery does not read settings or probe process activity.
func (a *API) NewAdminHandler(authenticate func(*http.Request) (string, bool), allowedOrigins ...string) (http.Handler, error) {
	allowedOrigins = append([]string(nil), allowedOrigins...)
	protect := func(r *http.Request) error {
		if !AdminOriginAllowed(r, allowedOrigins) {
			return &admin.Failure{Code: admin.Forbidden, Message: "An approved Origin is required."}
		}
		return nil
	}
	revision := "nanite.admin.v2.preferences"
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
	streamDefault, _ := admin.String("streaming")
	retentionDefault, _ := admin.Integer(15)
	definition := admin.Definition{
		App: admin.App{ID: "nanite", Label: "Nanite"}, Revision: revision, BasePath: "/api",
		Groups: []admin.Group{{ID: "preferences", Label: "Preferences", Scope: admin.Scope{Kind: "app", ID: "nanite"},
			Fields: []admin.Field{
				{Key: "tool_stream_behavior", Type: admin.StringType, Title: "Tool stream behavior", Required: true, Enum: streamEnum, Default: &streamDefault, Editable: true},
				{Key: "tool_drawer_retention", Type: admin.IntegerType, Title: "Tool drawer retention", Required: true, Enum: retentionEnum, Default: &retentionDefault, Editable: true},
			}, Resolution: admin.Resolution{Precedence: []admin.SourceKind{admin.OverrideSource, admin.DefaultSource}, WriteLayer: admin.OverrideSource},
			Capabilities: admin.Capabilities{CanRead: true, CanValidate: true, CanUpdate: true, CanReset: true}, Backend: adminPreferencesBackend{api: a, revision: revision},
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
		ProtectCommand: protect,
	})
	if err != nil {
		return nil, err
	}
	// Host policy checks every POST, including unknown routes, before body reads.
	// It also preserves the Basic challenge independently of optional global auth.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authErr := authorize(r, adminhttp.Resource{}); authErr != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="nanite"`)
			writeAdminPolicyFailure(w, http.StatusUnauthorized, admin.Unauthenticated, "Authentication is required.")
			return
		}
		if r.Method == http.MethodPost && protect(r) != nil {
			writeAdminPolicyFailure(w, http.StatusForbidden, admin.Forbidden, "An approved Origin is required.")
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
