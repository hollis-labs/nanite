package mcp

import (
	"context"
	//nolint:gosec // G501: md5 is exposed as a user-selectable option in the
	// `hash` MCP tool (algorithm=md5). It is not used for any security
	// decision inside nanite; callers who opt in are responsible for their
	// use case. See callHash below.
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ssrfResolver resolves a hostname to IP addresses. Tests replace this to
// control what IPs the dialer pins against without real DNS lookups.
type ssrfResolver func(ctx context.Context, host string) ([]net.IP, error)

// defaultSSRFResolver uses the system resolver.
func defaultSSRFResolver(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// GeneralToolsTransport provides general-purpose utility tools
// that are not path-scoped.
type GeneralToolsTransport struct {
	// AllowLocalhost permits connections to 127.0.0.0/8 and `localhost`. Off
	// by default; enable explicitly via configuration when the host process
	// legitimately needs to reach localhost services.
	AllowLocalhost bool

	// resolver is the DNS hook used by the SSRF guard. Nil falls back to
	// the system resolver. Tests install a stub here.
	resolver ssrfResolver

	// dialer is the low-level dial function used once the IP has been
	// validated. Nil uses net.Dialer with a short connect timeout. Tests
	// install a stub that records the dial target without opening a socket.
	dialer func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewGeneralToolsTransport creates a GeneralToolsTransport.
func NewGeneralToolsTransport() *GeneralToolsTransport {
	return &GeneralToolsTransport{}
}

// ssrfDeniedCIDRs is the set of IP ranges that web_fetch refuses to dial.
// Covers loopback, link-local (AWS/GCP/Azure IMDS at 169.254.169.254),
// RFC1918 private ranges, CGNAT, unspecified, IPv6 loopback, ULA, and IPv6
// link-local. Localhost is handled separately so AllowLocalhost can override.
var ssrfDeniedCIDRs = mustParseCIDRs([]string{
	"169.254.0.0/16", // link-local incl. cloud IMDS
	"10.0.0.0/8",     // RFC1918
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"0.0.0.0/8",      // unspecified
	"100.64.0.0/10",  // CGNAT
	"fc00::/7",       // IPv6 ULA
	"fe80::/10",      // IPv6 link-local
	"::/128",         // IPv6 unspecified
})

// ssrfLoopbackCIDRs covers 127.0.0.0/8 and ::1/128. Separated so the allow
// flag can gate them.
var ssrfLoopbackCIDRs = mustParseCIDRs([]string{
	"127.0.0.0/8",
	"::1/128",
})

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, block, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("ssrf: invalid CIDR %q: %v", c, err))
		}
		out = append(out, block)
	}
	return out
}

// errSSRFBlocked is the sentinel used for every SSRF-class rejection so
// callers (and tests) can distinguish validator errors from transport errors
// via errors.Is.
var errSSRFBlocked = errors.New("ssrf: blocked destination")

// ListTools returns the general utility tools.
func (g *GeneralToolsTransport) ListTools(_ context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name: "web_fetch",
			Description: "Fetch a URL via HTTP GET and return the response body as text.\n\n" +
				"**When to use:** When the user asks to retrieve content from a public URL — API endpoints, documentation pages, raw file URLs, or any web resource.\n\n" +
				"**When NOT to use:** Do not use for internal/private IPs, localhost, or cloud IMDS addresses (169.254.169.254) — those are blocked by SSRF guard. Do not use as a substitute for code or file operations already available via dev_read/dev_glob.\n\n" +
				"**Output shape:** Status line (\"Status: 200 OK\"), optional Content-Type, blank line, then the response body — truncated to 8000 chars if larger (up to 1 MiB is read from the server before truncation). Truncation is flagged with a [truncated: ...] suffix.\n\n" +
				"**Notes:** Many news/social sites block automated requests (403/Cloudflare). Works best with APIs, documentation sites, and raw content URLs. Example: web_fetch(url=\"https://api.github.com/repos/hollis-labs/nanite\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "Full URL including https://. Must be a public hostname — private IPs, localhost, and link-local addresses are blocked. Example: https://docs.anthropic.com/en/docs"},
				},
				"required": []string{"url"},
			},
		},
		{
			Name: "json_parse",
			Description: "Extract a value from a JSON string using dot-notation path (e.g. .data.items[0].name).\n\n" +
				"**When to use:** When you have a JSON blob (e.g. from a prior tool result) and need to extract a deeply nested field without writing code.\n\n" +
				"**When NOT to use:** Do not use for structured iteration over arrays — extract a specific field by path. If you need to loop, parse the full JSON yourself.\n\n" +
				"**Output shape:** The value at the path as a string (primitive), \"null\" for null, or a JSON-encoded sub-object/array.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"json": map[string]any{"type": "string", "description": "JSON string to parse"},
					"path": map[string]any{"type": "string", "description": "Dot-notation path (e.g. .data.items[0].name). Leading dot is optional."},
				},
				"required": []string{"json", "path"},
			},
		},
		{
			Name: "datetime",
			Description: "Get the current UTC time, or compute a date offset from now.\n\n" +
				"**When to use:** When you need to compute a future or past timestamp (e.g. deadline in 3 days, token expiry in 1 hour). The current date is already available in the system prompt for today's date questions — don't call this just to check the date.\n\n" +
				"**When NOT to use:** Do not call for simple \"what day is it\" questions — that is already in your context. Do not use for calendar arithmetic beyond day/week offsets.\n\n" +
				"**Output shape:** RFC3339 timestamp string, always UTC. Example: \"2026-04-29T15:00:00Z\".\n\n" +
				"**Supported units:** s (seconds), m (minutes), h (hours), d (days), w (weeks). Prefix with + or -. Examples: +3d, -1h, +30m, -2w.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"operation": map[string]any{"type": "string", "description": "Date math expression: <sign><n><unit> where sign is + or -, n is an integer, and unit is s/m/h/d/w. Examples: +3d, -1h, +30m. Omit to get the current time."},
				},
			},
		},
		{
			Name: "base64_encode",
			Description: "Encode a UTF-8 string to standard base64.\n\n" +
				"**When to use:** When you need to encode binary-safe data for HTTP headers, JSON payloads, or similar protocols.\n\n" +
				"**Output shape:** Base64 string (standard alphabet, no line wrapping). Chain with base64_decode to reverse.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "String to encode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name: "base64_decode",
			Description: "Decode a standard base64 string back to its original UTF-8 form.\n\n" +
				"**When to use:** When you received a base64-encoded value (e.g. from a credential store or API response) and need to read the plaintext.\n\n" +
				"**Output shape:** Decoded string. Returns an error if the input is not valid base64.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "Base64-encoded string to decode (standard alphabet)"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name: "url_encode",
			Description: "URL percent-encode a string for use in query parameters.\n\n" +
				"**When to use:** When you need to embed a user-supplied value in a URL query string safely (spaces → +, special chars → %XX).\n\n" +
				"**Output shape:** Percent-encoded string. Chain with url_decode to reverse.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "String to percent-encode for a URL query parameter"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name: "url_decode",
			Description: "Decode a URL percent-encoded string.\n\n" +
				"**When to use:** When you have a percent-encoded value from a URL and need to read it as plain text.\n\n" +
				"**Output shape:** Decoded string. Returns an error if the encoding is invalid.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "URL percent-encoded string to decode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name: "hash",
			Description: "Compute a hash of an input string.\n\n" +
				"**When to use:** When you need to verify integrity, derive a cache key, or produce a fingerprint. sha256 is the default and the recommended choice for any security-adjacent use.\n\n" +
				"**When NOT to use:** md5 is available as an explicit user request but is NOT suitable for security purposes (collision-prone). If you're computing a password or secret fingerprint, always use sha256.\n\n" +
				"**Output shape:** Lowercase hex-encoded hash string. Example: \"a591a6d40bf420404a011733cfb7b190d62c65bf0bcda32b57b277d9ad9f146e\" (sha256 of \"Hello World\").",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input":     map[string]any{"type": "string", "description": "String to hash"},
					"algorithm": map[string]any{"type": "string", "description": "Hash algorithm: sha256 (default, recommended) or md5 (not for security use)"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name: "math_eval",
			Description: "Evaluate a basic arithmetic expression and return the numeric result.\n\n" +
				"**When to use:** When the user asks for arithmetic that would be imprecise if done in the LLM's head — percentage calculations, exponentiation, or multi-step formulas.\n\n" +
				"**When NOT to use:** Do not use for statistics, matrix math, or string operations — this is arithmetic only. Do not pass expressions longer than 1 KiB (the cap will return an error).\n\n" +
				"**Supported operators:** + - * / ^ (power), unary minus, parentheses, and float literals. Example: \"(100 * 0.08) + 15.5\" → \"23.5\". No functions (no sqrt, sin, etc.).\n\n" +
				"**Output shape:** A number as a string — integer if the result is whole (e.g. \"42\"), floating-point otherwise (e.g. \"3.14159\").",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"expression": map[string]any{"type": "string", "description": "Arithmetic expression to evaluate. Max 1 KiB. Supports +, -, *, /, ^ (power), parentheses, and float literals. Example: \"(100 * 0.08) + 15.5\""},
				},
				"required": []string{"expression"},
			},
		},
		{
			Name: "think",
			Description: "A scratchpad for organizing reasoning before acting — not a data-fetching tool.\n\n" +
				"**When to use:** When you want to reason through a multi-step problem, plan a sequence of tool calls, or review what you already know before committing to an approach. Useful before complex queries where a wrong choice would waste round-trips.\n\n" +
				"**When NOT to use:** Do NOT use as a substitute for actual tool calls — thinking about data you haven't fetched does not make the data available. If you need information, call the tool that provides it. Do not use to \"remember\" something across turns — use scratchpad_write for intra-turn state, or Vanta memory tools for cross-session state.\n\n" +
				"**Output shape:** Always returns \"Thought recorded.\" — the server stores nothing. The value is the structured reasoning you produce inside the call itself.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thought": map[string]any{"type": "string", "description": "Your internal reasoning, plan, or analysis. Write this as if explaining your approach to another engineer."},
				},
				"required": []string{"thought"},
			},
		},
	}, nil
}

// Tunable bounds for general tools.
const (
	// webFetchBodyCap bounds the response body returned to the LLM in bytes.
	// This is the post-sanitization cap applied before assembling the text
	// result. An upstream server cannot force the nanite process to allocate
	// more than this for a single web_fetch call.
	webFetchBodyCap = 1 << 20 // 1 MiB

	// webFetchExposedCap is the chars-of-body shown to the LLM. The body is
	// read up to webFetchBodyCap, then sliced to this for the result payload.
	// Separating "dialed-in cap" from "exposed cap" keeps the LLM context
	// reasonable while still letting us detect and flag truncation.
	webFetchExposedCap = 8000

	// maxMathDepth is the recursion depth limit for the math_eval parser.
	// Crossing it returns an error rather than risking a stack-overflow
	// fatal from deeply-nested input.
	maxMathDepth = 100

	// maxMathExprLen is the byte cap for a user-supplied math expression.
	// 1 KiB is far above any legitimate arithmetic input and rules out the
	// length-based variant of the same DoS.
	maxMathExprLen = 1024
)

// CallTool dispatches to the appropriate handler.
func (g *GeneralToolsTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch name {
	case "web_fetch":
		return g.callWebFetch(ctx, args)
	case "json_parse":
		return g.callJSONParse(args)
	case "datetime":
		return g.callDatetime(args)
	case "base64_encode":
		return g.callBase64Encode(args)
	case "base64_decode":
		return g.callBase64Decode(args)
	case "url_encode":
		return g.callURLEncode(args)
	case "url_decode":
		return g.callURLDecode(args)
	case "hash":
		return g.callHash(args)
	case "math_eval":
		return g.callMathEval(args)
	case "think":
		return g.callThink(args)
	default:
		return ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func (g *GeneralToolsTransport) callWebFetch(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ErrorResult(fmt.Sprintf("cancelled: %v", err)), nil
	}
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return ErrorResult("url is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid url: %v", err)), nil
	}

	// Scheme allowlist. Reject file://, ftp://, gopher://, etc. Go's default
	// transport handles only http/https anyway, but we reject explicitly so
	// the error message is informative and no clever transport injection
	// reaches the dialer.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrorResult(fmt.Sprintf("unsupported scheme %q (http/https only)", parsed.Scheme)), nil
	}
	if parsed.Host == "" {
		return ErrorResult("url missing host"), nil
	}

	// Build a Transport whose DialContext resolves the hostname once, checks
	// every returned IP against the SSRF denylist, and pins the connection
	// to the validated IP. This is the standard defense against DNS
	// rebinding: the DNS answer we checked is the exact IP we dial.
	resolver := g.resolver
	if resolver == nil {
		resolver = defaultSSRFResolver
	}
	innerDial := g.dialer
	if innerDial == nil {
		d := &net.Dialer{Timeout: 5 * time.Second}
		innerDial = d.DialContext
	}
	allowLocal := g.AllowLocalhost

	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			return nil, splitErr
		}
		// Reject `localhost` and `*.localhost` unless explicitly allowed.
		if !allowLocal && isLocalhostName(host) {
			return nil, fmt.Errorf("%w: localhost name %q", errSSRFBlocked, host)
		}
		ips, resolveErr := resolver(ctx, host)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w: no IPs for %q", errSSRFBlocked, host)
		}
		for _, ip := range ips {
			if !allowLocal {
				for _, block := range ssrfLoopbackCIDRs {
					if block.Contains(ip) {
						return nil, fmt.Errorf("%w: loopback %s", errSSRFBlocked, ip)
					}
				}
			}
			for _, block := range ssrfDeniedCIDRs {
				if block.Contains(ip) {
					return nil, fmt.Errorf("%w: %s in %s", errSSRFBlocked, ip, block)
				}
			}
			if ip.IsUnspecified() {
				return nil, fmt.Errorf("%w: unspecified %s", errSSRFBlocked, ip)
			}
		}
		// Pin to the first validated IP. DNS cannot rebind between the
		// resolver call above and the dial below because we supply a literal
		// address, not a name.
		pinned := ips[0].String()
		return innerDial(ctx, network, net.JoinHostPort(pinned, port))
	}

	transport := &http.Transport{
		DialContext:           dial,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		DisableKeepAlives:     true,
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		// Cap redirects at 3 and re-validate scheme on each hop. The dialer
		// re-runs the IP check for each new request so redirect targets get
		// the same protection as the original URL.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}

	resp, fetchErr := fetchWithRetry(ctx, client, rawURL)
	if fetchErr != nil {
		// Format a structured error result the model can act on.
		detail := fetchErr.Detail
		if detail == "" {
			detail = "no additional detail"
		}
		msg := fmt.Sprintf(
			"fetch_error: kind=%s url=%s attempts=%d",
			fetchErr.Kind, fetchErr.URL, fetchErr.Attempts,
		)
		if fetchErr.Status != 0 {
			msg += fmt.Sprintf(" status=%d", fetchErr.Status)
		}
		msg += fmt.Sprintf(" detail=%s", detail)

		// Append model-actionable hint per error kind.
		switch fetchErr.Kind {
		case FetchErrBlocked:
			msg += "\nhint: site is blocking automated requests (anti-bot / WAF). Try a different URL or ask the user."
		case FetchErrEmptyHTML:
			msg += "\nhint: page appears JS-rendered or behind an anti-bot gate. The tier-1 fetch cannot bypass this — try an API endpoint or a different source."
		case FetchErr5xxAfterRetries:
			msg += "\nhint: server returned 5xx on all attempts. The site may be temporarily down — retry later or try a different URL."
		case FetchErrTimeout:
			msg += "\nhint: request timed out. The site may be slow or unreachable — try a more specific or smaller URL."
		case FetchErrDNS:
			msg += "\nhint: DNS resolution failed. Check that the hostname is correct."
		case FetchErrTLS:
			msg += "\nhint: TLS/certificate error. The site may have a misconfigured certificate."
		case FetchErrRedirectLoop:
			msg += "\nhint: too many redirects. Try the final destination URL directly."
		}
		return ErrorResult(msg), nil
	}
	defer resp.Body.Close()

	// Read up to webFetchBodyCap bytes from the upstream. This caps the
	// envelope allocation regardless of what Content-Length the peer sent or
	// whether the peer sent chunked data without a length header.
	limited := io.LimitReader(resp.Body, int64(webFetchBodyCap)+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read error: %v", err)), nil
	}
	truncatedByBody := len(raw) > webFetchBodyCap
	if truncatedByBody {
		raw = raw[:webFetchBodyCap]
	}

	// Detect empty-HTML: a tiny HTML response is almost always a JS-rendered
	// stub or anti-bot gate. Surface it as a structured error rather than
	// returning near-empty content that would confuse the model.
	rawCT := resp.Header.Get("Content-Type")
	if classifyEmptyHTML(rawCT, len(raw)) {
		fe := &FetchError{
			Kind:     FetchErrEmptyHTML,
			URL:      rawURL,
			Status:   resp.StatusCode,
			Attempts: 1,
			Detail:   fmt.Sprintf("body=%d bytes (threshold %d) content-type=%s", len(raw), emptyHTMLThreshold, rawCT),
		}
		msg := fmt.Sprintf(
			"fetch_error: kind=%s url=%s attempts=%d status=%d detail=%s",
			fe.Kind, fe.URL, fe.Attempts, fe.Status, fe.Detail,
		)
		msg += "\nhint: page appears JS-rendered or behind an anti-bot gate. The tier-1 fetch cannot bypass this — try an API endpoint or a different source."
		return ErrorResult(msg), nil
	}

	// Sanitize upstream bytes before they are stitched into the tool
	// envelope. Three concerns:
	//   1. Envelope markers (<!--ENVELOPE_DATA:...-->) a malicious upstream
	//      may plant in the body to smuggle forged UI primitives into the
	//      conversation. Strip them with a neutralized tag.
	//   2. Control characters that the MCP text envelope cannot represent
	//      cleanly — most notably ANSI escape sequences and NUL bytes.
	//      Strip ASCII control bytes other than \t, \n, \r.
	//   3. Invalid UTF-8 sequences that would break downstream JSON encoding.
	//      strings.ToValidUTF8 replaces them with U+FFFD.
	sanitized := sanitizeWebBody(raw)

	// Limit the exposed slice to webFetchExposedCap characters. Runes outside
	// ASCII count as multi-byte here; the cap is defensive rather than precise.
	displayed := sanitized
	truncatedByExposed := false
	if len(displayed) > webFetchExposedCap {
		displayed = displayed[:webFetchExposedCap]
		truncatedByExposed = true
	}

	// Compose a status line that sanitizes the upstream response text as well.
	// A peer can smuggle control bytes via a bespoke Status string; scrub it
	// the same way the body is scrubbed.
	statusText := sanitizeEnvelopeField(resp.Status)

	var out strings.Builder
	fmt.Fprintf(&out, "Status: %d %s\n", resp.StatusCode, statusText)

	// Expose a minimal, sanitized Content-Type so the LLM can reason about
	// the payload without the full untrusted header set flowing through.
	ct := sanitizeEnvelopeField(resp.Header.Get("Content-Type"))
	if ct != "" {
		fmt.Fprintf(&out, "Content-Type: %s\n", ct)
	}
	out.WriteString("\n")
	out.WriteString(displayed)

	if truncatedByExposed || truncatedByBody {
		fmt.Fprintf(&out, "\n[truncated: %d chars shown; body cap %d bytes]", webFetchExposedCap, webFetchBodyCap)
	}

	return TextResult(out.String()), nil
}

// envelopeMarkerRE matches the <!--ENVELOPE_DATA:...:ENVELOPE_DATA--> marker
// the chat engine uses to capture structured UI data from tool output. Any
// upstream response that embeds such a marker (deliberately or otherwise)
// would be treated as a trusted envelope by the chat engine, so web_fetch
// scrubs them on the way in.
var envelopeMarkerRE = regexp.MustCompile(`(?s)<!--\s*ENVELOPE_DATA:.*?:ENVELOPE_DATA\s*-->`)

// sanitizeWebBody applies the web_fetch defense-in-depth output filter.
func sanitizeWebBody(b []byte) string {
	// 1. Valid UTF-8.
	s := strings.ToValidUTF8(string(b), "\uFFFD")
	// 2. Strip envelope markers.
	s = envelopeMarkerRE.ReplaceAllString(s, "[envelope marker removed]")
	// 3. Strip ASCII control chars except tab, newline, CR. This removes
	// ANSI escape sequences (ESC = 0x1B) and NUL without mangling normal
	// text or unicode content.
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\t', '\n', '\r':
			sb.WriteRune(r)
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// sanitizeEnvelopeField scrubs a single header-like string: collapses control
// characters and runs the envelope-marker scrub so a crafted header cannot
// smuggle structural content into the tool result envelope.
func sanitizeEnvelopeField(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = envelopeMarkerRE.ReplaceAllString(s, "[envelope marker removed]")
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r == '\t' {
			sb.WriteRune(' ')
			continue
		}
		if r == '\n' || r == '\r' {
			sb.WriteRune(' ')
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		sb.WriteRune(r)
	}
	return strings.TrimSpace(sb.String())
}

// isLocalhostName matches "localhost" and any subdomain of ".localhost"
// (RFC 6761 reserves both). Case-insensitive.
func isLocalhostName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

func (g *GeneralToolsTransport) callJSONParse(args map[string]any) (*ToolResult, error) {
	jsonStr, _ := args["json"].(string)
	pathStr, _ := args["path"].(string)
	if jsonStr == "" || pathStr == "" {
		return ErrorResult("json and path are required"), nil
	}

	// Parse JSON into any.
	var data any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return ErrorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	// Navigate the path.
	parts := parseDotPath(pathStr)
	current := data
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part.key]
			if !ok {
				return ErrorResult(fmt.Sprintf("key %q not found", part.key)), nil
			}
			current = val
		case []any:
			if part.index < 0 || part.index >= len(v) {
				return ErrorResult(fmt.Sprintf("index %d out of range (length %d)", part.index, len(v))), nil
			}
			current = v[part.index]
		default:
			return ErrorResult(fmt.Sprintf("cannot navigate into %T at %q", current, part.key)), nil
		}
	}

	// Format the result.
	switch v := current.(type) {
	case string:
		return TextResult(v), nil
	case nil:
		return TextResult("null"), nil
	default:
		out, _ := json.Marshal(v)
		return TextResult(string(out)), nil
	}
}

type pathSegment struct {
	key   string
	index int // -1 if not an array access
}

// parseDotPath parses ".data.items[0].name" into segments.
func parseDotPath(path string) []pathSegment {
	// Strip leading dot.
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return nil
	}

	var segments []pathSegment
	parts := strings.Split(path, ".")
	for _, part := range parts {
		// Check for array index: "items[0]"
		if idx := strings.Index(part, "["); idx >= 0 {
			key := part[:idx]
			indexStr := strings.TrimSuffix(part[idx+1:], "]")
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				// Treat as plain key.
				segments = append(segments, pathSegment{key: part, index: -1})
				continue
			}
			if key != "" {
				segments = append(segments, pathSegment{key: key, index: -1})
			}
			segments = append(segments, pathSegment{key: "", index: index})
		} else {
			segments = append(segments, pathSegment{key: part, index: -1})
		}
	}
	return segments
}

func (g *GeneralToolsTransport) callDatetime(args map[string]any) (*ToolResult, error) {
	now := time.Now().UTC()

	op, _ := args["operation"].(string)
	if op == "" {
		return TextResult(now.Format(time.RFC3339)), nil
	}

	duration, err := parseDateMath(op)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	result := now.Add(duration)
	return TextResult(result.Format(time.RFC3339)), nil
}

// parseDateMath parses expressions like "+3d", "-1h", "+30m", "-2w".
func parseDateMath(expr string) (time.Duration, error) {
	if len(expr) < 2 {
		return 0, fmt.Errorf("invalid date math expression: %q", expr)
	}

	sign := 1
	rest := expr
	if expr[0] == '+' {
		rest = expr[1:]
	} else if expr[0] == '-' {
		sign = -1
		rest = expr[1:]
	}

	if len(rest) < 2 {
		return 0, fmt.Errorf("invalid date math expression: %q", expr)
	}

	unit := rest[len(rest)-1]
	numStr := rest[:len(rest)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid number in date math: %q", numStr)
	}

	var multiplier time.Duration
	switch unit {
	case 's':
		multiplier = time.Second
	case 'm':
		multiplier = time.Minute
	case 'h':
		multiplier = time.Hour
	case 'd':
		multiplier = 24 * time.Hour
	case 'w':
		multiplier = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unknown unit %q (use s/m/h/d/w)", string(unit))
	}

	return time.Duration(sign*n) * multiplier, nil
}

func (g *GeneralToolsTransport) callBase64Encode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return ErrorResult("input is required"), nil
	}
	return TextResult(base64.StdEncoding.EncodeToString([]byte(input))), nil
}

func (g *GeneralToolsTransport) callBase64Decode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return ErrorResult("input is required"), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return ErrorResult(fmt.Sprintf("decode error: %v", err)), nil
	}
	return TextResult(string(decoded)), nil
}

func (g *GeneralToolsTransport) callURLEncode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return ErrorResult("input is required"), nil
	}
	return TextResult(url.QueryEscape(input)), nil
}

func (g *GeneralToolsTransport) callURLDecode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return ErrorResult("input is required"), nil
	}
	decoded, err := url.QueryUnescape(input)
	if err != nil {
		return ErrorResult(fmt.Sprintf("decode error: %v", err)), nil
	}
	return TextResult(decoded), nil
}

func (g *GeneralToolsTransport) callHash(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return ErrorResult("input is required"), nil
	}
	algorithm, _ := args["algorithm"].(string)
	if algorithm == "" {
		algorithm = "sha256"
	}

	switch algorithm {
	case "sha256":
		h := sha256.Sum256([]byte(input))
		return TextResult(hex.EncodeToString(h[:])), nil
	case "md5":
		// md5 is offered as an explicit, user-requested algorithm choice in
		// the tool contract. Not used internally for any security purpose
		// (integrity, authentication, cache-key collision-resistance).
		//nolint:gosec // G401: user-requested algorithm, non-security use.
		h := md5.Sum([]byte(input))
		return TextResult(hex.EncodeToString(h[:])), nil
	default:
		return ErrorResult(fmt.Sprintf("unsupported algorithm %q (use sha256 or md5)", algorithm)), nil
	}
}

func (g *GeneralToolsTransport) callMathEval(args map[string]any) (*ToolResult, error) {
	expr, _ := args["expression"].(string)
	if expr == "" {
		return ErrorResult("expression is required"), nil
	}
	if len(expr) > maxMathExprLen {
		return ErrorResult(fmt.Sprintf("expression too long: %d bytes (max %d)", len(expr), maxMathExprLen)), nil
	}

	result, err := evalExpr(expr)
	if err != nil {
		return ErrorResult(fmt.Sprintf("eval error: %v", err)), nil
	}

	// Format nicely: show integer if whole number.
	if result == float64(int64(result)) && !math.IsInf(result, 0) {
		return TextResult(strconv.FormatInt(int64(result), 10)), nil
	}
	return TextResult(strconv.FormatFloat(result, 'g', -1, 64)), nil
}

func (g *GeneralToolsTransport) callThink(args map[string]any) (*ToolResult, error) {
	raw, exists := args["thought"]
	if !exists || raw == nil {
		return ErrorResult("thought is required"), nil
	}
	thought, ok := raw.(string)
	if !ok {
		thought = fmt.Sprintf("%v", raw)
	}
	if thought == "" {
		return ErrorResult("thought is required"), nil
	}
	return TextResult("Thought recorded."), nil
}

// --- math expression parser (recursive descent) ---

type mathParser struct {
	input string
	pos   int
	depth int
}

// errMathDepthExceeded is the sentinel returned when the parser's recursion
// depth exceeds maxMathDepth. Surfaced to tests so they can assert on the
// exact failure mode.
var errMathDepthExceeded = fmt.Errorf("expression too deeply nested (max depth %d)", maxMathDepth)

// enter increments the recursion-depth counter and returns an error when the
// cap is exceeded. The caller is responsible for calling leave() on return.
func (p *mathParser) enter() error {
	p.depth++
	if p.depth > maxMathDepth {
		return errMathDepthExceeded
	}
	return nil
}

func (p *mathParser) leave() { p.depth-- }

func evalExpr(expr string) (float64, error) {
	p := &mathParser{input: strings.TrimSpace(expr)}
	result, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) {
		return 0, fmt.Errorf("unexpected character at position %d: %q", p.pos, string(p.input[p.pos]))
	}
	return result, nil
}

func (p *mathParser) parseExpr() (float64, error) {
	if err := p.enter(); err != nil {
		return 0, err
	}
	defer p.leave()
	return p.parseAddSub()
}

func (p *mathParser) parseAddSub() (float64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '+' && op != '-' {
			return left, nil
		}
		p.pos++
		right, err := p.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

func (p *mathParser) parseMulDiv() (float64, error) {
	left, err := p.parsePower()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' {
			return left, nil
		}
		p.pos++
		right, err := p.parsePower()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
	}
}

func (p *mathParser) parsePower() (float64, error) {
	if err := p.enter(); err != nil {
		return 0, err
	}
	defer p.leave()
	base, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '^' {
		p.pos++
		exp, err := p.parsePower() // right-associative
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *mathParser) parseUnary() (float64, error) {
	if err := p.enter(); err != nil {
		return 0, err
	}
	defer p.leave()
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '-' {
		p.pos++
		val, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return -val, nil
	}
	if p.pos < len(p.input) && p.input[p.pos] == '+' {
		p.pos++
		return p.parseUnary()
	}
	return p.parseAtom()
}

func (p *mathParser) parseAtom() (float64, error) {
	if err := p.enter(); err != nil {
		return 0, err
	}
	defer p.leave()
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}

	// Parenthesized expression.
	if p.input[p.pos] == '(' {
		p.pos++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return val, nil
	}

	// Number.
	start := p.pos
	for p.pos < len(p.input) && (unicode.IsDigit(rune(p.input[p.pos])) || p.input[p.pos] == '.') {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("unexpected character: %q", string(p.input[p.pos]))
	}
	return strconv.ParseFloat(p.input[start:p.pos], 64)
}

func (p *mathParser) skipSpaces() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}
