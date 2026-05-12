package service

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strings"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	"github.com/hollis-labs/nanite/internal/runtime/agent/recovery"
	"github.com/hollis-labs/nanite/internal/safego"
)

// HTTP-stream cause + error-class constants synthesized when the chat
// HTTP-provider path interrupts a turn and notifies the recovery broker.
//
// These are *nanite-side* causes — the lib's agentsessions.Cause* set is
// CLI-supervisor-driven (idle_timeout, watchdog_kill, etc.) and doesn't
// model HTTP streaming. The classifier doesn't branch on these directly;
// they fall through to the default Code != 0 path (Transient on first
// attempt, Permanent on the second). The breadcrumb still gets the
// authoritative cause string so postmortem queries can pivot off
// http_stream_* values.
//
// CW-20260512-0001.
const (
	// causeHTTPStreamTimeout is the synthesized ExitError.Cause for chat
	// stream interrupts caused by ctx.Err() == DeadlineExceeded.
	causeHTTPStreamTimeout = "http_stream_timeout"

	// causeHTTPStreamRateBudget is the synthesized ExitError.Cause for
	// rate-budget or compaction-recoverable failures that the recovery
	// pipeline refused to repair.
	causeHTTPStreamRateBudget = "http_stream_rate_budget"

	// causeHTTPStreamTransport is the synthesized ExitError.Cause for
	// transport-layer errors (DNS / TCP / TLS / read EOF) surfacing out
	// of the provider's HTTP client.
	causeHTTPStreamTransport = "http_stream_transport"

	// causeHTTPStreamHTTPStatus is the synthesized ExitError.Cause for
	// non-2xx responses returned by the provider (5xx, 4xx other than
	// 401/403). The classifier-side stderr-substring rule covers auth
	// failures (401/403/unauthorized) separately when the message string
	// is threaded through StderrTail.
	causeHTTPStreamHTTPStatus = "http_stream_http_status"

	// causeHTTPStream is the generic synthesized cause for in-stream
	// errors with no better classification.
	causeHTTPStream = "http_stream"
)

// HTTP-stream meta keys threaded into the recovery broker's meta bag.
// Read by postmortem queries against nanite_recovery_breadcrumbs; not
// consumed by the broker's classifier (which only reads the standard
// recovery.MetaKey* keys).
const (
	httpStreamMetaKeySource     = "source"
	httpStreamMetaKeyErrorClass = "error_class"
)

// httpStreamMetaSource is the canonical value for
// httpStreamMetaKeySource. Distinguishes broker breadcrumbs originating
// from this code path from CLI-subagent breadcrumbs (which omit source).
const httpStreamMetaSource = "http_chat_stream"

// persistPartialAssistantAndNotifyBroker funnels every chat HTTP-stream
// error path through a single helper: persist the `[generation
// interrupted]` row AND notify the recovery broker.
//
// streamErr is the in-stream error (ctx.Err, provider.StreamChat err,
// llmtypes.StreamEvent.Error string wrapped via fmt.Errorf, etc.).
// classifyHTTPStreamError derives the synthetic ExitError.Cause and the
// error_class meta value from it; a nil streamErr falls through to the
// generic http_stream cause.
//
// providerName is the resolved chat provider name (anthropic / openai /
// gemini-api / openrouter / etc.). Threaded into the broker meta bag so
// postmortem queries can pivot on provider.
//
// The broker call runs on a safego.Go goroutine — OnSessionExit may
// dispatch a replacement session via agent.Boot, which is too heavy to
// run on the SSE-response-closing path. The chat-side response is
// already finalized when this helper fires (every call site is
// followed by `return`); the breadcrumb write + envelope emit can
// happen out of band.
//
// nil-safe on every front: when agentDeps is unwired (legacy chat
// harness path) or Recovery is nil, the helper degrades to a plain
// persistPartialAssistant call. CW-20260512-0001.
func (s *chatServiceImpl) persistPartialAssistantAndNotifyBroker(
	ctx context.Context,
	sessionID, assistantMsgID, agentID, content, providerName string,
	streamErr error,
) {
	// Always persist the partial-assistant row first; the broker
	// notification is best-effort observability on top of that.
	s.persistPartialAssistant(sessionID, assistantMsgID, agentID, content)

	if s.agentDeps == nil || s.agentDeps.Recovery == nil {
		return
	}

	cause, errorClass := classifyHTTPStreamError(streamErr)

	exit := &agentsessions.ExitError{
		// Code -1 mirrors how the lib's buildExitError encodes
		// signal-driven terminations: we don't have a real process exit
		// code (HTTP failure, not a subagent crash), but the classifier
		// default branch keys on Code != 0 so -1 carries us into the
		// "unclassified non-zero exit" path. Attempt-aware escalation
		// (first occurrence → Transient, second → Permanent) handles
		// retry loops naturally.
		Code:  -1,
		Cause: cause,
	}

	// stderrTail surfaces the underlying error string so the
	// classifier's auth-failure substring rule (401/403/unauthorized)
	// fires when applicable — provider HTTP errors frequently carry
	// these markers in their message text.
	stderrTail := ""
	if streamErr != nil {
		stderrTail = streamErr.Error()
	}

	meta := map[string]any{
		recovery.MetaKeyProvider:    providerName,
		recovery.MetaKeyMode:        "http_chat", // distinct from "long_lived" / "one_shot" — HTTP path has no agent.Mode
		recovery.MetaKeyStderrTail:  stderrTail,
		httpStreamMetaKeySource:     httpStreamMetaSource,
		httpStreamMetaKeyErrorClass: errorClass,
	}

	broker := s.agentDeps.Recovery

	slog.Info("recovery: http chat stream error — invoking broker",
		"session_id", sessionID,
		"provider", providerName,
		"cause", cause,
		"error_class", errorClass)

	safego.Go(ctx, "service.chat.recovery.http-notify", func() {
		broker.OnSessionExit(sessionID, exit, meta)
	})
}

// classifyHTTPStreamError derives a synthesized ExitError.Cause and a
// human-friendly error_class label from a chat-HTTP-stream error.
//
// Returns (causeHTTPStream, "unknown") for nil errors so callers can
// thread ctx.Err() unconditionally; the broker still writes a
// breadcrumb in that case (postmortem coverage trumps a pristine
// telemetry tape).
//
// Classification precedence:
//
//  1. context.DeadlineExceeded / context.Canceled  → timeout
//  2. llmcontracts.ErrRequestExceedsRateBudget     → rate_budget
//  3. net.Error.Timeout() == true                  → timeout
//  4. net.*Error / url.Error / DNSError            → transport
//  5. stderr substring "401" / "403" / "unauthorized"
//                                                  → auth (defers cause
//                                                    string to http_status
//                                                    so the classifier's
//                                                    StderrTail rule fires)
//  6. stderr substring "5xx" / "server error"      → http_status
//  7. default                                      → unknown
func classifyHTTPStreamError(err error) (cause, errorClass string) {
	if err == nil {
		return causeHTTPStream, "unknown"
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return causeHTTPStreamTimeout, "timeout"
	}

	if errors.Is(err, llmcontracts.ErrRequestExceedsRateBudget) {
		return causeHTTPStreamRateBudget, "rate_budget"
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return causeHTTPStreamTimeout, "timeout"
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return causeHTTPStreamTransport, "transport"
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return causeHTTPStreamTransport, "transport"
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return causeHTTPStreamTransport, "transport"
	}

	// Fall through to substring matching for errors that arrive as
	// plain string-wrapped messages (mid-stream evt.Error in chat_generate.go).
	lower := strings.ToLower(err.Error())

	// Rate-limit / 429 — provider error strings frequently include "rate"
	// or "429". chat.ClassifyError already keys on these for the
	// FE-facing error code; mirror the heuristic here for the broker
	// breadcrumb.
	if strings.Contains(lower, "429") || strings.Contains(lower, "rate limit") {
		return causeHTTPStreamRateBudget, "rate_limit"
	}

	// Auth — defer to the classifier's StderrTail substring rule
	// (401/403/unauthorized → RemediationRefreshCredentials). The cause
	// is http_status so the breadcrumb's class is still informative;
	// the classifier picks up the actual remediation from the stderr
	// tail we already threaded into meta.
	if strings.Contains(lower, "401") ||
		strings.Contains(lower, "403") ||
		strings.Contains(lower, "unauthorized") {
		return causeHTTPStreamHTTPStatus, "auth"
	}

	// HTTP status — coarse substring match for 5xx / "server error" /
	// "internal server error". Provider SDKs format these differently;
	// the goal is bucketing for postmortem queries, not exhaustive
	// matching.
	if strings.Contains(lower, "5xx") ||
		strings.Contains(lower, "server error") ||
		strings.Contains(lower, "internal server error") ||
		strings.Contains(lower, " 500 ") ||
		strings.Contains(lower, " 502 ") ||
		strings.Contains(lower, " 503 ") ||
		strings.Contains(lower, " 504 ") {
		return causeHTTPStreamHTTPStatus, "http_5xx"
	}

	// Timeouts arriving as plain strings (provider-specific wording).
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") {
		return causeHTTPStreamTimeout, "timeout"
	}

	// Transport-shaped strings without a typed net.Error wrap.
	if strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "eof") {
		return causeHTTPStreamTransport, "transport"
	}

	return causeHTTPStream, "unknown"
}
