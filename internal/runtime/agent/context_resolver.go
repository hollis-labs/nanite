package agent

// context_resolver.go — Phase 2 item 02
// (TASKS/phase-2/02-port-forward-dynamic-resolver.md): the launch-time
// execution glue for the DB-configured cmd/http dynamic-resolver
// capability architecture/02-agent-launching.md names as a first-class
// mechanism "available to every agent, not gated behind a separate
// catalog system."
//
// Pre-port, this capability lived only inside internal/bootprofile
// (requirements.go / slots.go / agentcontext_adapter.go), reachable only
// for a session whose provider was an encoded "bootprofile:<id>" id.
// internal/bootprofile is retired in full by
// TASKS/phase-2/04-retire-boot-profile-catalog.md; this file is the new,
// generally-available home.
//
// # Reuse, not reimplementation
//
// The actual cmd-exec / HTTP-fetch execution — including every safety
// property (cmd timeout + hard ceiling, stderr-tail capture, HTTP
// timeout + body-size cap, status-code validation) — already lives in
// the shared, already-vendored github.com/hollis-labs/agentkit module's
// agentcontext/resolvers package. Nanite's boot-profile catalog never
// reimplemented that logic; it built a thin SlotSpec-conversion adapter
// on top (internal/bootprofile/agentcontext_adapter.go). This file is
// the same shape of adapter, aimed at store.AgentContextResolver rows
// instead of boot-profile-catalog Requirement structs.
//
// role_summary / skill_index are deliberately NOT wired here — see
// TASKS/phase-2/02-port-forward-dynamic-resolver.md's Work Log for the
// disposition (this task's scope is cmd/http only, per the architecture
// doc).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/agentcontext/resolvers"
	"github.com/hollis-labs/nanite/internal/store"
)

// ResolveContextBlocks drains a list of DB-configured
// agent_context_resolvers rows at launch time, executing each cmd/http
// resolver through the shared go-agent-context resolvers package, and
// returns the resolved bodies keyed by slot name.
//
// workdir is the base directory cmd resolvers run in (CWD defaulting
// when a row's CWD is empty) and is threaded through as the shared
// resolver's ContextRequest.Workdir.
//
// Any single resolver failure aborts the WHOLE call and returns a
// pointed error naming the offending slot — mirroring the pre-port
// boot-profile behavior (bootprofile.ResolveRequirements /
// assembleRequirements): a half-resolved boot prompt is worse than a
// clean stop. Callers that would rather degrade a single broken
// resolver instead of failing the whole boot should filter rows before
// calling this function — that policy question is deliberately left to
// the caller, not decided here.
func ResolveContextBlocks(ctx context.Context, rows []store.AgentContextResolver, workdir string) (map[string]string, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	res := map[agentcontext.SlotSourceKind]agentcontext.Resolver{
		agentcontext.SlotSourceKindCmd:      resolvers.NewCmdResolver(),
		agentcontext.SlotSourceKindHTTPText: resolvers.NewHTTPTextResolver(),
		agentcontext.SlotSourceKindHTTPJSON: resolvers.NewHTTPJSONResolver(),
	}
	provider, err := agentcontext.NewProvider(res, agentcontext.DefaultRenderer{})
	if err != nil {
		return nil, fmt.Errorf("agent context resolver: build provider: %w", err)
	}

	slots := make([]agentcontext.SlotSpec, 0, len(rows))
	for _, row := range rows {
		spec, convErr := contextResolverToSlotSpec(row)
		if convErr != nil {
			return nil, convErr
		}
		slots = append(slots, spec)
	}

	creq := agentcontext.ContextRequest{Slots: slots, Workdir: workdir}
	result, err := provider.Assemble(ctx, creq)
	if err != nil {
		return nil, fmt.Errorf("agent context resolver: assemble: %w", err)
	}

	out := make(map[string]string, len(result.Slots))
	for _, sr := range result.Slots {
		if sr.Err != nil {
			return nil, fmt.Errorf("agent context resolver: slot %q resolution failed: %w", sr.Name, sr.Err)
		}
		out[sr.Name] = sr.Content
	}
	return out, nil
}

// contextResolverToSlotSpec converts a store.AgentContextResolver row
// into the shared agentcontext.SlotSpec shape. Mapping (mirrors the
// pre-port bootprofile.requirementToSlotSpec):
//
//	cmd  → SlotSourceKindCmd          (Run/CWD/Timeout)
//	http → SlotSourceKindHTTPJSON when ResponseFormat == "json",
//	       otherwise SlotSourceKindHTTPText (URL/Headers[/JSONPath])
//
// Slots are NOT marked Required — an empty-but-successful resolution is
// tolerated (the block is simply omitted from the rendered prompt),
// matching the pre-port deferred-slot behavior.
func contextResolverToSlotSpec(row store.AgentContextResolver) (agentcontext.SlotSpec, error) {
	spec := agentcontext.SlotSpec{Name: row.SlotName}
	switch row.Kind {
	case "cmd":
		timeout, err := parseResolverTimeout(row.Timeout)
		if err != nil {
			return agentcontext.SlotSpec{}, fmt.Errorf("slot %q: %w", row.SlotName, err)
		}
		spec.Source = agentcontext.SlotSource{
			Kind: agentcontext.SlotSourceKindCmd,
			Cmd:  agentcontext.CmdSource{Run: row.Run, CWD: row.CWD, Timeout: timeout},
		}
	case "http":
		timeout, err := parseResolverTimeout(row.Timeout)
		if err != nil {
			return agentcontext.SlotSpec{}, fmt.Errorf("slot %q: %w", row.SlotName, err)
		}
		headers, err := parseResolverHeaders(row.HeadersJSON)
		if err != nil {
			return agentcontext.SlotSpec{}, fmt.Errorf("slot %q: %w", row.SlotName, err)
		}
		if row.ResponseFormat == "json" {
			spec.Source = agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPJSON,
				HTTPJSON: agentcontext.HTTPJSONSource{URL: row.URL, Timeout: timeout, Headers: headers, JSONPath: row.JSONPath},
			}
		} else {
			spec.Source = agentcontext.SlotSource{
				Kind:     agentcontext.SlotSourceKindHTTPText,
				HTTPText: agentcontext.HTTPTextSource{URL: row.URL, Timeout: timeout, Headers: headers},
			}
		}
	default:
		return agentcontext.SlotSpec{}, fmt.Errorf("slot %q: resolver kind %q not supported (want cmd or http)", row.SlotName, row.Kind)
	}
	return spec, nil
}

// parseResolverTimeout converts a store.AgentContextResolver.Timeout
// string (e.g. "5s", "2m") into a time.Duration. Empty means "resolver
// default" (zero duration → the shared resolver's own
// DefaultCmdTimeout / DefaultHTTPTimeout). A malformed string is a hard
// error so a typo in the DB row surfaces immediately rather than
// silently defaulting.
func parseResolverTimeout(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", s, err)
	}
	return d, nil
}

// parseResolverHeaders decodes an AgentContextResolver.HeadersJSON
// string into the map[string]string the shared HTTP resolvers expect.
// Empty / "{}" returns a nil map (no extra headers).
func parseResolverHeaders(raw string) (map[string]string, error) {
	if raw == "" || raw == "{}" {
		return nil, nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil, fmt.Errorf("invalid headers_json: %w", err)
	}
	return headers, nil
}
