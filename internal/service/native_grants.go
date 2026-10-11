package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Native-view tool authority (CW-20261011-0037). This decides whether a tool may
// be offered to, or run for, a view Nanite hosts. It is deliberately separate
// from the fabric-actor path (ListAgentToolNames / GetAgentForActor): neither
// answers for the other.
//
// The decision is made on every dispatch, so revoking a grant, an authority or
// the submitting credential refuses the next call in a turn already running. A
// call that has already started cannot be recalled.

// CallerStanding is what the host currently knows about the credential that
// submitted a turn.
type CallerStanding struct {
	Valid bool // exists, not revoked, not expired
	Tools bool // holds the tools scope (operate alone only submits turns)
}

// CallerStandingReader looks a credential up by id at the moment of asking. It
// must not return a cached verdict.
type CallerStandingReader interface {
	CallerStanding(ctx context.Context, credentialID string) (CallerStanding, error)
}

// NativeToolGrantReader resolves the grants of the live local authority that
// matches the host settings revision and pin now in force.
type NativeToolGrantReader interface {
	ResolveToolNames(ctx context.Context, hostSettingsID string) ([]string, error)
}

// NativeSubject names the view and the credential that submitted the turn. The
// credential id is looked up on every decision, never trusted as a verdict.
type NativeSubject struct {
	HostSettingsID string
	SubmittedBy    string
}

// ToolNarrowing restricts a turn to a subset of the baseline grants. A nil
// narrowing leaves the baseline as it is; an empty non-nil one allows nothing.
type ToolNarrowing map[string]bool

// NativeGrants decides native-view tool authority.
type NativeGrants struct {
	Grants  NativeToolGrantReader
	Callers CallerStandingReader
}

const (
	nativeDenyCaller   = "the submitting credential is no longer valid"
	nativeDenyScope    = "the submitting credential cannot run tools"
	nativeDenyGrant    = "tool not granted to this native view"
	nativeDenyNarrowed = "tool not permitted for this turn"
	nativeDenyUnready  = "native tool authorization unavailable"
)

// effective returns the tools the view may use right now for this caller, and
// the reason when there are none because of the caller.
func (g *NativeGrants) effective(ctx context.Context, subj NativeSubject, narrow ToolNarrowing) ([]string, string, error) {
	if g == nil || g.Grants == nil || g.Callers == nil || subj.HostSettingsID == "" || subj.SubmittedBy == "" {
		return nil, nativeDenyUnready, errors.New("native grants are not fully wired")
	}
	standing, err := g.Callers.CallerStanding(ctx, subj.SubmittedBy)
	if err != nil {
		return nil, nativeDenyUnready, fmt.Errorf("caller standing: %w", err)
	}
	if !standing.Valid {
		return nil, nativeDenyCaller, nil
	}
	if !standing.Tools {
		return nil, nativeDenyScope, nil
	}
	names, err := g.Grants.ResolveToolNames(ctx, subj.HostSettingsID)
	if err != nil {
		return nil, nativeDenyUnready, err
	}
	if narrow == nil {
		return names, "", nil
	}
	kept := make([]string, 0, len(names))
	for _, n := range names {
		if narrow[n] {
			kept = append(kept, n)
		}
	}
	return kept, "", nil
}

// Offered lists the tools to present to the model for a turn. An operate-only
// caller, a revoked caller and any lookup failure all produce a tool-free turn.
func (g *NativeGrants) Offered(ctx context.Context, subj NativeSubject, narrow ToolNarrowing) []string {
	names, _, err := g.effective(ctx, subj, narrow)
	if err != nil {
		slog.Warn("native-grants: offering no tools", "host_settings", subj.HostSettingsID, "err", err)
		return nil
	}
	return names
}

// AuthorizeDispatch is the per-call decision. It fails closed.
func (g *NativeGrants) AuthorizeDispatch(ctx context.Context, subj NativeSubject, narrow ToolNarrowing, tool string) (bool, string) {
	names, why, err := g.effective(ctx, subj, narrow)
	if err != nil {
		slog.Warn("native-grants: refusing dispatch", "host_settings", subj.HostSettingsID, "tool", tool, "err", err)
		return false, why
	}
	if why != "" {
		return false, why
	}
	for _, n := range names {
		if n == tool {
			return true, ""
		}
	}
	if narrow != nil && !narrow[tool] {
		// Distinguish "not in this turn's narrowing" from "never granted" only
		// when the baseline would have allowed it.
		if base, _, berr := g.effective(ctx, subj, nil); berr == nil {
			for _, n := range base {
				if n == tool {
					return false, nativeDenyNarrowed
				}
			}
		}
	}
	return false, nativeDenyGrant
}
