package reactions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// internalAPICallConfig is selftool_reactions.config's shape for an
// internal_api_call row — {endpoint, method, body_template} per
// docs/engineering/architecture/11-harness-reactive-self-tools.md's
// worked-example section.
type internalAPICallConfig struct {
	Endpoint     string         `json:"endpoint"`
	Method       string         `json:"method"`
	BodyTemplate map[string]any `json:"body_template"`
}

// executeInternalAPICall issues the HTTP request an internal_api_call
// reaction describes: substitutes payload's fields into config's
// body_template (same {{key}} substitution as render_card's template),
// marshals it to a JSON body, and issues it against config's endpoint
// with e's httpClient.
//
// Request-issuing mechanism (documented per this task's own Done-means
// requirement — see this task file's Work Log for the full reasoning): a
// plain same-process HTTP client call against endpoint exactly as
// configured, not a direct in-process handler invocation. endpoint is
// expected to already be a fully-qualified, same-process URL (e.g.
// "http://127.0.0.1:<port>/api/example/task-updates"), matching how
// cmd/nanite/main.go's own apiBaseURL is already threaded, pre-resolved,
// into every other same-process HTTP consumer in this codebase
// (WorkflowContextAssembler, ExternalWorkflowEngineConfig.APIBaseURL,
// AgentCardGenerator) rather than each consumer independently discovering
// or joining a base URL + path. This package does not know its own
// process's listen port and is not given one — resolving endpoint into a
// full URL is the seeding caller's responsibility (illustrated by
// 07-worked-example-task-update-report.md's seed config), not this
// executor's.
//
// A non-2xx response is treated as a failed, non-fatal reaction (returned
// as an error here, which Fire records as OutcomeError without aborting
// its loop over sibling reactions) — matching this task's Done-means
// requirement exactly. Auth/retries/idempotency are explicitly out of
// scope (TASKS/harness-reactive-self-tools/README.md's "What this batch
// does NOT do").
func (e *Engine) executeInternalAPICall(ctx context.Context, configJSON string, payload map[string]any) error {
	var cfg internalAPICallConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("internal_api_call: parse config: %w", err)
	}
	if cfg.Endpoint == "" {
		return fmt.Errorf("internal_api_call: config missing endpoint")
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}

	resolvedBody := substituteTemplate(cfg.BodyTemplate, payload)
	bodyJSON, err := json.Marshal(resolvedBody)
	if err != nil {
		return fmt.Errorf("internal_api_call: marshal resolved body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfg.Endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("internal_api_call: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("internal_api_call: request to %s failed: %w", cfg.Endpoint, err)
	}
	defer func() {
		_ = resp.Body.Close() // Response-body close is best-effort cleanup after the request result is read.
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Bounded read: internal_api_call targets a trusted, same-process
		// endpoint by construction (unlike external_api_call/callback,
		// this kind never leaves that trust boundary), but bound the
		// error-body read anyway rather than io.ReadAll an unbounded
		// response into an error string.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("internal_api_call: %s %s returned non-2xx status %d: %s",
			method, cfg.Endpoint, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	// Drain/discard the response body on success — this reaction kind has
	// no payload to surface (see FiredReaction.Payload's doc comment in
	// result.go); Fire only needs to know it succeeded.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
