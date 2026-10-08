// Package subagent binds the embeddable lifecycle service to Nanite host ports.
package subagent

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
	core "github.com/hollis-labs/substrate/agent/subagent"
)

type SettingsReader interface {
	GetUserSettings(context.Context) (*store.UserSettings, error)
}
type SettingsAdapter struct{ Reader SettingsReader }

func (a SettingsAdapter) GetUserSettings(ctx context.Context) (*core.Settings, error) {
	s, err := a.Reader.GetUserSettings(ctx)
	if err != nil || s == nil {
		return nil, err
	}
	return &core.Settings{SubagentApprovalRequired: s.SubagentApprovalRequired, SubagentApprovalTimeoutSeconds: s.SubagentApprovalTimeoutSeconds, DeveloperMode: s.DeveloperMode}, nil
}

type ProfileReader interface {
	GetAgentBySlug(context.Context, string) (*store.AgentProfile, error)
}
type ProfileAdapter struct{ Reader ProfileReader }

func (a ProfileAdapter) GetAgentBySlug(ctx context.Context, slug string) (*core.Profile, error) {
	p, err := a.Reader.GetAgentBySlug(ctx, slug)
	if err != nil || p == nil {
		return nil, err
	}
	return &core.Profile{ID: p.ID, Slug: p.Slug, CanExecute: p.CanExecute}, nil
}

// Authorizer maps the existing host trust decision without creating authority.
// Refusal retains dispatch.ErrUntrustedRole; lookup failure retains the existing
// normal-tier approval posture. Empty profile IDs stay normal.
type Authorizer struct{ Resolver dispatch.TrustResolver }

func (a Authorizer) AuthorizeSpawn(ctx context.Context, id string) (core.SpawnAuthorization, error) {
	if a.Resolver == nil || id == "" {
		return core.SpawnAuthorization{}, nil
	}
	tier, err := a.Resolver.ResolveTrust(ctx, id)
	if err != nil {
		return core.SpawnAuthorization{}, err
	}
	if tier == dispatch.TrustUntrusted {
		return core.SpawnAuthorization{Refusal: dispatch.ErrUntrustedRole}, nil
	}
	return core.SpawnAuthorization{BypassApproval: tier == dispatch.TrustTrusted}, nil
}

// NewService supplies host configuration and panic observation. Lifecycle
// methods and data types remain owned by the substrate agent module.
func NewService(db core.Database, runner core.Runner, poster core.MessagePoster, approver core.ApprovalEmitter, settings SettingsReader) *core.Service {
	var settingsPort core.SettingsReader
	if settings != nil {
		settingsPort = SettingsAdapter{settings}
	}
	svc := core.NewService(db, runner, poster, approver, settingsPort)
	var resolver dispatch.TrustResolver
	if r, ok := settings.(dispatch.TrustResolver); ok {
		resolver = r
	}
	svc.SetSpawnAuthorizer(Authorizer{resolver})
	svc.SetLivenessResolvers(resolveDefaultTimeoutSeconds, resolveHeartbeatInterval)
	svc.SetPanicReporter(safego.ReportRecovered)
	return svc
}

// defaultTimeoutEnvVar is the operator knob for the wall-clock backstop
// budget applied to a subagent run that does not carry an explicit
// per-call timeout. Mirrors Torque's profile/env tiering. A value
// outside [core.MinTimeoutSeconds, core.MaxTimeoutSeconds] is ignored with a
// warning so a typo can't silently disable the backstop.
const defaultTimeoutEnvVar = "NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS"

// timeoutInRange reports whether secs is a usable subagent-run timeout.

// resolveDefaultTimeoutSeconds picks the wall-clock backstop budget for
// a subagent run that did not supply an explicit per-call timeout, in
// priority order (mirrors Torque resolveTimeout, timeout.go:35-43):
//
//  1. NANITE_SUBAGENT_DEFAULT_TIMEOUT_SECONDS when set and within
//     [core.MinTimeoutSeconds, core.MaxTimeoutSeconds]
//  2. core.DefaultTimeoutSeconds (the compiled-in 1800s floor)
//
// An explicit, in-range req.TimeoutSeconds still takes precedence over
// both — that check stays in Spawn, ahead of this call. An env value
// that is unparseable or out of range is ignored (with a warning) so a
// misconfiguration falls back safely rather than disabling the backstop.
func resolveDefaultTimeoutSeconds() int {
	raw := strings.TrimSpace(os.Getenv(defaultTimeoutEnvVar))
	if raw == "" {
		return core.DefaultTimeoutSeconds
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("subagent: ignoring non-integer timeout override env var",
			"env", defaultTimeoutEnvVar, "value", raw, "fallback_seconds", core.DefaultTimeoutSeconds)
		return core.DefaultTimeoutSeconds
	}
	if !core.TimeoutInRange(secs) {
		slog.Warn("subagent: ignoring out-of-range timeout override env var",
			"env", defaultTimeoutEnvVar, "value", secs,
			"min", core.MinTimeoutSeconds, "max", core.MaxTimeoutSeconds,
			"fallback_seconds", core.DefaultTimeoutSeconds)
		return core.DefaultTimeoutSeconds
	}
	return secs
}

// heartbeatSecondsEnvVar is the operator knob for the heartbeat cadence.
const heartbeatSecondsEnvVar = "NANITE_SUBAGENT_HEARTBEAT_SECONDS"

// heartbeatIntervalInRange reports whether secs is a usable heartbeat
// cadence (0 is handled separately by the caller as "disabled").

// resolveHeartbeatInterval picks the "still running" ping cadence for a
// subagent run, in priority order:
//
//  1. NANITE_SUBAGENT_HEARTBEAT_SECONDS == "0" → heartbeats disabled
//     (returns 0).
//  2. NANITE_SUBAGENT_HEARTBEAT_SECONDS set to another in-range value →
//     that value.
//  3. unset, unparseable, or out of range → core.DefaultHeartbeatSeconds.
func resolveHeartbeatInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(heartbeatSecondsEnvVar))
	if raw == "" {
		return core.DefaultHeartbeatSeconds * time.Second
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("subagent: ignoring non-integer heartbeat override env var",
			"env", heartbeatSecondsEnvVar, "value", raw, "fallback_seconds", core.DefaultHeartbeatSeconds)
		return core.DefaultHeartbeatSeconds * time.Second
	}
	if secs == 0 {
		return 0
	}
	if !core.HeartbeatIntervalInRange(secs) {
		slog.Warn("subagent: ignoring out-of-range heartbeat override env var",
			"env", heartbeatSecondsEnvVar, "value", secs,
			"min", core.MinHeartbeatSeconds, "max", core.MaxHeartbeatSeconds,
			"fallback_seconds", core.DefaultHeartbeatSeconds)
		return core.DefaultHeartbeatSeconds * time.Second
	}
	return time.Duration(secs) * time.Second
}
