package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// FetchErrorKind classifies the failure mode for a web_fetch attempt so the
// model can reason about whether to retry with a different URL, simplify the
// query, or give up.
type FetchErrorKind string

const (
	// FetchErrBlocked — HTTP 4xx anti-bot / access-denied response (non-429),
	// or a Cloudflare / WAF challenge page.
	FetchErrBlocked FetchErrorKind = "blocked"

	// FetchErrTimeout — the overall wall-clock budget or a per-attempt deadline
	// was exceeded.
	FetchErrTimeout FetchErrorKind = "timeout"

	// FetchErrEmptyHTML — HTTP 200 with a text/html body shorter than the
	// minimum threshold, indicating a JS-rendered page or anti-bot gate that
	// delivered a stub page.
	FetchErrEmptyHTML FetchErrorKind = "empty_html"

	// FetchErr5xxAfterRetries — the server returned 5xx on every attempt.
	FetchErr5xxAfterRetries FetchErrorKind = "5xx_after_retries"

	// FetchErrDNS — DNS resolution failed (NXDOMAIN, SERVFAIL, or no IPs
	// returned that passed the SSRF guard).
	FetchErrDNS FetchErrorKind = "dns_failure"

	// FetchErrTLS — TLS handshake failed (certificate error, version mismatch,
	// or similar).
	FetchErrTLS FetchErrorKind = "tls_error"

	// FetchErrRedirectLoop — too many redirects (> maxFetchRedirects).
	FetchErrRedirectLoop FetchErrorKind = "redirect_loop"

	// FetchErr4xx — non-retryable 4xx other than 403/blocked-flavor. E.g.
	// 404 Not Found or 410 Gone.
	FetchErr4xx FetchErrorKind = "4xx"
)

// FetchError is the structured failure type returned by the retry loop.
// It is surfaced in the MCP tool result so the model can decide its next move.
type FetchError struct {
	Kind     FetchErrorKind
	URL      string
	Status   int    // HTTP status code if applicable; 0 if pre-HTTP failure
	Attempts int    // number of attempts before giving up
	Detail   string // optional human-readable context
}

func (e *FetchError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("web_fetch %s: kind=%s status=%d attempts=%d detail=%s",
			e.URL, e.Kind, e.Status, e.Attempts, e.Detail)
	}
	return fmt.Sprintf("web_fetch %s: kind=%s attempts=%d detail=%s",
		e.URL, e.Kind, e.Attempts, e.Detail)
}

// Fetch resilience configuration. All values are package-level constants so
// they are easy to audit and override in tests via the helper constructors.
const (
	// fetchMaxRetries is the maximum number of retry attempts (not counting the
	// initial try). Total attempts = fetchMaxRetries + 1.
	fetchMaxRetries = 3

	// fetchBaseDelay is the initial backoff duration before jitter.
	fetchBaseDelay = 500 * time.Millisecond

	// fetchWallBudget is the maximum total elapsed time for all attempts
	// combined. The retry loop cancels early if this budget is exceeded.
	fetchWallBudget = 10 * time.Second

	// emptyHTMLThreshold is the minimum response body size (bytes) for a
	// text/html response to be considered non-empty. Responses smaller than
	// this are classified as FetchErrEmptyHTML.
	emptyHTMLThreshold = 200
)

// fetchUserAgents is the rotation pool. We use recent but unremarkable UAs —
// no search-engine bots, no crawlers, no impersonation of indexers.
var fetchUserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4.1 Safari/605.1.15",
}

// pickUserAgent returns a random UA from the rotation pool.
// Using math/rand (not crypto/rand) is intentional — this is not a security
// decision, just load distribution across UA strings.
func pickUserAgent() string {
	//nolint:gosec // G404: non-security random selection of a UA string.
	return fetchUserAgents[rand.Intn(len(fetchUserAgents))]
}

// retryableStatusCode reports whether an HTTP status code should trigger a
// retry. We retry on 429 (rate-limited) and 5xx server errors; we do NOT
// retry on other 4xx codes.
func retryableStatusCode(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	// Retry all 5xx that aren't individually listed above.
	return status >= 500 && status < 600
}

// retryableNetError reports whether a network-layer error should trigger a
// retry. Connection resets, EOF on read, and generic timeouts are retried.
// Context cancellation and DNS failures are NOT retried.
func retryableNetError(err error) bool {
	if err == nil {
		return false
	}
	// Never retry user / context cancellation.
	if errors.Is(err, context.Canceled) {
		return false
	}
	// Context deadline exceeded may be our own per-attempt timeout → retry.
	// (The outer wall-budget check will catch cases where we're truly out of
	// time before queuing another attempt.)
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Classify by net.Error behavior.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary() //nolint:staticcheck // Temporary still useful here
	}
	// EOF from an abrupt connection close (common for rate-limited responses
	// or load-balancer resets).
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// syscall.ECONNRESET, EPIPE, etc. surface as *net.OpError with an
	// underlying *os.SyscallError. The Timeout()/Temporary() branch above
	// usually catches them but the string check is a belt-and-suspenders
	// fallback for environments where Temporary() is not set.
	s := err.Error()
	return strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection refused") // usually transient in retry window
}

// classifyNetError maps a dial/request error to a FetchErrorKind.
func classifyNetError(err error) FetchErrorKind {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return FetchErrTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FetchErrTimeout
	}

	s := err.Error()

	// TLS errors surface with recognizable keywords before the net layer.
	if strings.Contains(s, "tls:") ||
		strings.Contains(s, "certificate") ||
		strings.Contains(s, "x509:") {
		return FetchErrTLS
	}

	// DNS: go surfaces these as *net.DNSError; also catch "no such host".
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return FetchErrDNS
	}
	if strings.Contains(s, "no such host") ||
		strings.Contains(s, "NXDOMAIN") ||
		strings.Contains(s, "dns") {
		return FetchErrDNS
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return FetchErrTimeout
	}

	return FetchErrTimeout // safe default for unknown transport errors
}

// retryAfterDelay parses the Retry-After header (seconds or HTTP-date) and
// returns the indicated wait duration. Returns 0 if the header is absent or
// unparseable.
func retryAfterDelay(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	// Integer seconds.
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	// HTTP-date.
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// backoffDelay returns the sleep duration for the given attempt number
// (0-indexed). It applies exponential backoff with ±25% jitter to avoid the
// thundering-herd problem.
//
// attempt=0: ~500ms, attempt=1: ~1s, attempt=2: ~2s.
func backoffDelay(attempt int) time.Duration {
	base := fetchBaseDelay * (1 << uint(attempt)) //nolint:gosec // G115: always positive
	// Jitter ±25%: pick a random factor in [0.75, 1.25].
	//nolint:gosec // G404: non-security jitter math.
	jitterFactor := 0.75 + rand.Float64()*0.5
	return time.Duration(float64(base) * jitterFactor)
}

// fetchWithRetry executes an HTTP GET with retry-with-backoff and UA rotation.
// It returns the final *http.Response on success, or a *FetchError on all
// failure paths. The caller is responsible for closing resp.Body on success.
//
// client must be pre-configured with the SSRF-guarded transport. The rawURL
// must have already been validated (scheme, host) by the caller.
func fetchWithRetry(ctx context.Context, client *http.Client, rawURL string) (*http.Response, *FetchError) {
	deadline := time.Now().Add(fetchWallBudget)

	var lastFetchErr *FetchError

	for attempt := 0; attempt <= fetchMaxRetries; attempt++ {
		// Check wall-budget before each attempt.
		if time.Now().After(deadline) {
			if lastFetchErr != nil {
				lastFetchErr.Attempts = attempt
				lastFetchErr.Detail += " (wall budget exhausted)"
				return nil, lastFetchErr
			}
			return nil, &FetchError{
				Kind:     FetchErrTimeout,
				URL:      rawURL,
				Attempts: attempt,
				Detail:   "wall budget exhausted before first attempt",
			}
		}

		// Also check context before sending.
		if ctx.Err() != nil {
			return nil, &FetchError{
				Kind:     FetchErrTimeout,
				URL:      rawURL,
				Attempts: attempt,
				Detail:   fmt.Sprintf("context: %v", ctx.Err()),
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			// Request construction failure is non-retryable.
			return nil, &FetchError{
				Kind:     FetchErr4xx,
				URL:      rawURL,
				Attempts: attempt + 1,
				Detail:   fmt.Sprintf("request construction: %v", err),
			}
		}
		req.Header.Set("User-Agent", pickUserAgent())
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")

		resp, doErr := client.Do(req)
		if doErr != nil {
			// Context canceled → never retry, propagate immediately.
			if errors.Is(doErr, context.Canceled) {
				return nil, &FetchError{
					Kind:     FetchErrTimeout,
					URL:      rawURL,
					Attempts: attempt + 1,
					Detail:   "context canceled",
				}
			}
			// SSRF block → never retry.
			if errors.Is(doErr, errSSRFBlocked) {
				return nil, &FetchError{
					Kind:     FetchErrBlocked,
					URL:      rawURL,
					Attempts: attempt + 1,
					Detail:   fmt.Sprintf("ssrf blocked: %v", doErr),
				}
			}

			kind := classifyNetError(doErr)
			lastFetchErr = &FetchError{
				Kind:     kind,
				URL:      rawURL,
				Attempts: attempt + 1,
				Detail:   doErr.Error(),
			}

			if !retryableNetError(doErr) || attempt == fetchMaxRetries {
				return nil, lastFetchErr
			}

			sleep(ctx, backoffDelay(attempt))
			continue
		}

		// --- HTTP response received ---

		// Success path.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		// Redirect loop (CheckRedirect already fired but just in case).
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			_ = resp.Body.Close() // The rejected redirect response is discarded before returning its redirect error.
			return nil, &FetchError{
				Kind:     FetchErrRedirectLoop,
				URL:      rawURL,
				Status:   resp.StatusCode,
				Attempts: attempt + 1,
				Detail:   "unexpected redirect after redirect limit",
			}
		}

		// 429 — rate limited; respect Retry-After if present.
		if resp.StatusCode == http.StatusTooManyRequests {
			ra := retryAfterDelay(resp.Header)
			_ = resp.Body.Close() // The retryable response is discarded before the next attempt.
			lastFetchErr = &FetchError{
				Kind:     FetchErr5xxAfterRetries,
				URL:      rawURL,
				Status:   resp.StatusCode,
				Attempts: attempt + 1,
				Detail:   "rate limited (429)",
			}
			if attempt == fetchMaxRetries {
				return nil, lastFetchErr
			}
			delay := backoffDelay(attempt)
			if ra > 0 && ra < fetchWallBudget {
				delay = ra
			}
			sleep(ctx, delay)
			continue
		}

		// Other retryable 5xx.
		if retryableStatusCode(resp.StatusCode) {
			_ = resp.Body.Close() // The retryable 5xx response is discarded before retry or terminal error.
			lastFetchErr = &FetchError{
				Kind:     FetchErr5xxAfterRetries,
				URL:      rawURL,
				Status:   resp.StatusCode,
				Attempts: attempt + 1,
				Detail:   fmt.Sprintf("server error %d", resp.StatusCode),
			}
			if attempt == fetchMaxRetries {
				return nil, lastFetchErr
			}
			sleep(ctx, backoffDelay(attempt))
			continue
		}

		// Non-retryable 4xx.
		kind := FetchErr4xx
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			kind = FetchErrBlocked
		}
		_ = resp.Body.Close() // The non-retryable response body is discarded before returning its status error.
		return nil, &FetchError{
			Kind:     kind,
			URL:      rawURL,
			Status:   resp.StatusCode,
			Attempts: attempt + 1,
			Detail:   fmt.Sprintf("non-retryable %d", resp.StatusCode),
		}
	}

	// Should not reach here; return last error.
	if lastFetchErr != nil {
		return nil, lastFetchErr
	}
	return nil, &FetchError{
		Kind:     FetchErrTimeout,
		URL:      rawURL,
		Attempts: fetchMaxRetries + 1,
		Detail:   "retry loop exhausted",
	}
}

// sleep waits for d, honoring context cancellation.
func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// classifyEmptyHTML returns true when a successful HTML response looks like a
// JS-rendered stub or anti-bot gate (body too small to be real content).
func classifyEmptyHTML(contentType string, bodyLen int) bool {
	ct := strings.ToLower(contentType)
	if !strings.Contains(ct, "text/html") {
		return false
	}
	return bodyLen < emptyHTMLThreshold
}
