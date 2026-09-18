package mcp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	gmcpclient "github.com/hollis-labs/go-mcp/client"
)

// defaultCallTimeout is the safety-net context deadline applied to a
// ListTools/CallTool call when the caller's own context carries none.
// stdio keeps its historical 30s (a local process either answers in
// milliseconds or is wedged; StdioTransport's old default). streamable_http
// and sse keep their historical 60s (StreamableClientTransport's own POST
// plus, for sse, the standalone GET stream's per-call read). A caller that
// supplies its own deadline is always respected in full, even past these --
// unlike HTTPTransport's old client.Timeout, which silently cut off a
// caller's longer deadline at a hardcoded 60s.
var defaultCallTimeout = map[string]time.Duration{
	gmcpclient.TransportStdio: 30 * time.Second,
	gmcpclient.TransportHTTP:  60 * time.Second,
	gmcpclient.TransportSSE:   60 * time.Second,
}

// remoteTransport adapts a server registered on Manager's shared
// go-mcp/client.Pool to the MCPTransport interface, translating between the
// official SDK's tool/result types (which the Pool speaks) and nanite's own
// Tool/ToolResult.
//
// One type now covers what used to be three (StdioTransport, HTTPTransport,
// SSETransport): the Pool already owns per-transport dialing, reconnect, and
// health-probing uniformly, so the only thing left to vary by kind here is
// the call timeout default above.
type remoteTransport struct {
	pool *gmcpclient.Pool
	name string
	kind string // one of gmcpclient.TransportStdio/TransportHTTP/TransportSSE
}

func newRemoteTransport(pool *gmcpclient.Pool, name, kind string) *remoteTransport {
	return &remoteTransport{pool: pool, name: name, kind: kind}
}

// withCallTimeout applies defaultCallTimeout when ctx carries no deadline of
// its own; a caller-supplied deadline is always left as-is.
func (t *remoteTransport) withCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultCallTimeout[t.kind])
}

// ListTools lists the server's tools.
//
// Unlike CallTool, a failure here is retried once after invalidating the
// connection: tools/list is read-only, so re-issuing it against a fresh
// connection cannot double an effect. This mirrors the SSE transport's prior
// reasoning, generalized to every remote kind now that the mechanism (Pool's
// connect-or-reuse plus Invalidate) is the same for all of them.
func (t *remoteTransport) ListTools(ctx context.Context) ([]Tool, error) {
	callCtx, cancel := t.withCallTimeout(ctx)
	defer cancel()

	res, err := t.pool.ListTools(callCtx, t.name)
	if err != nil {
		t.pool.Invalidate(t.name)
		res, err = t.pool.ListTools(callCtx, t.name)
	}
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	out := make([]Tool, 0, len(res.Tools))
	for _, tool := range res.Tools {
		if tool == nil {
			continue
		}
		converted, err := convertSDKTool(tool)
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		out = append(out, converted)
	}
	return out, nil
}

// CallTool invokes a tool and converts the result.
//
// A failed call is not retried by this adapter, with one narrow exception
// (isProvablyUnsent, below). A connection error generally cannot distinguish
// "the request never arrived" from "the reply did not come back", and
// re-issuing a tool call that may already have run is the wrong side to err
// on -- the same reasoning the SSE transport already applied, now uniform
// across every remote kind. go-mcp/client's own retry policy is configured
// at zero for this reason (see Manager's Pool construction); the Pool still
// tears the connection down on any error (see its own doc), so the next
// call reconnects.
func (t *remoteTransport) CallTool(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	callCtx, cancel := t.withCallTimeout(ctx)
	defer cancel()

	res, _, err := t.pool.CallTool(callCtx, t.name, name, arguments)
	if err != nil && isProvablyUnsent(err) {
		t.pool.Invalidate(t.name)
		res, _, err = t.pool.CallTool(callCtx, t.name, name, arguments)
	}
	if err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}
	return convertSDKCallResult(res), nil
}

// isProvablyUnsent reports whether err is the one CallTool failure shape
// that is safe to retry despite the general "ambiguous send state" policy
// above: the official SDK's jsonrpc2.Connection.Call rejects a request with
// an error whose message contains "client is closing" in exactly three
// cases (see its shuttingDown check) -- an explicit Close already in
// progress, a prior write failure, or (the common case here) the read side
// already having failed, e.g. because a stdio server's subprocess died
// between calls. All three checks run and return BEFORE the connection ever
// writes the request to the wire -- unlike every other CallTool failure,
// there is no ambiguity about whether the call reached the server: it
// didn't, so retrying after invalidating the stale connection cannot double
// an effect.
//
// This closes the gap CW-20260918's Agent Mux incident traced end to end:
// a stdio server (no periodic liveness probe -- see go-mcp/client's
// maybeProbeLocked, which explicitly skips TransportStdio) whose subprocess
// died during an idle period fails its next real call with exactly this
// message, and previously stayed failed until some other caller happened to
// invalidate the connection.
func isProvablyUnsent(err error) bool {
	return err != nil && strings.Contains(err.Error(), "client is closing")
}

// SetMaxResponseBytes forwards the tier-derived cap to the underlying
// go-mcp/client.Client. Manager.AddServer calls this immediately after
// registration, before any call has connected the server, so the cap is
// always in place before the first dial (see Client.SetMaxResponseBytes).
func (t *remoteTransport) SetMaxResponseBytes(n int) {
	c, err := t.pool.Get(t.name)
	if err != nil {
		return
	}
	c.SetMaxResponseBytes(n)
}

// Close tears the connection down AND deregisters it from the Pool -- the
// semantics Manager.RemoveServer's `interface{ Close() error }` assertion
// expects: once this returns, nothing about this server persists anywhere,
// including in the Pool's own bookkeeping.
func (t *remoteTransport) Close() error {
	return t.pool.Deregister(t.name)
}

// Restart tears the connection down WITHOUT deregistering it -- the Pool's
// lazy dial-on-first-use means the next call transparently respawns.
// RestartStdioTransports calls this, not Close: it wants the wedged process
// gone, not the server's registration removed (the same distinction
// StdioTransport.Close() used to blur, since it was called for both
// purposes by two different callers).
func (t *remoteTransport) Restart() {
	t.pool.Invalidate(t.name)
}

func (t *remoteTransport) isStdio() bool { return t.kind == gmcpclient.TransportStdio }

// resolveStdioEnv computes the subprocess environment for a stdio server:
// only the explicitly allowlisted host env var names, plus the per-server
// declared env, which wins on a key collision. Nothing else from nanite's
// own process environment is inherited -- closes a credential-leak vector
// (AWS_*, GITHUB_TOKEN, etc. never leak to a plugin subprocess unless
// explicitly allowlisted). Ported from the former StdioTransport's
// buildSubprocessEnv, as a map instead of a "KEY=VALUE" slice: a map
// deterministically resolves a key present in both the allowlist and the
// declared env to the declared value, where the old slice-based approach
// could produce two entries for the same key and left which one "won" to
// platform-dependent exec/env behavior.
//
// env is the per-server user-declared "KEY=VALUE" list from
// MCPServerConfig.Env; envAllowlist is the host env var names permitted to
// inherit.
func resolveStdioEnv(env []string, envAllowlist []string) map[string]string {
	resolved := make(map[string]string, len(envAllowlist)+len(env))
	for _, key := range envAllowlist {
		if v, ok := os.LookupEnv(key); ok {
			resolved[key] = v
		}
	}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			resolved[k] = v
		}
	}
	return resolved
}

// naniteCommandEnv is the go-mcp/client.WithCommandEnv callback Manager's
// Pool is configured with: it overrides the package default (inherit the
// whole host environment) with nanite's stricter allowlist discipline.
// cfg.Env has already been fully resolved by resolveStdioEnv at
// registration time (see AddStdioServer), so this only needs to flatten it
// deterministically and re-check the PATH requirement against the resolved
// result.
func naniteCommandEnv(cfg gmcpclient.ServerConfig) ([]string, error) {
	if !pathResolvable(cfg) {
		return nil, fmt.Errorf(
			"mcp stdio: %q is a bare command but its resolved env carries no PATH",
			cfg.Command,
		)
	}
	env := make([]string, 0, len(cfg.Env))
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return env, nil
}

// pathResolvable reports whether cfg's subprocess will have a PATH to
// resolve cfg.Command against: either an explicit PATH entry in cfg.Env
// (which resolveStdioEnv already folded the allowlist into), or cfg.Command
// itself being path-qualified, in which case exec skips LookPath entirely.
func pathResolvable(cfg gmcpclient.ServerConfig) bool {
	if strings.ContainsRune(cfg.Command, '/') {
		return true
	}
	_, ok := cfg.Env["PATH"]
	return ok
}
