package mcp

import (
	"context"
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
			Name:        "web_fetch",
			Description: "Fetch a URL via HTTP GET and return the response body as text. Returns status code and body truncated at 8000 chars. Note: many news/social sites block automated requests (403/Cloudflare). Works best with APIs, documentation sites, and raw content URLs. Example: web_fetch(url=\"https://api.github.com/repos/hollis-labs/nanite\")",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "Full URL including https://. Example: https://docs.anthropic.com/en/docs"},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "json_parse",
			Description: "Extract a value from a JSON string using dot-notation path (e.g. .data.items[0].name).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"json": map[string]any{"type": "string", "description": "JSON string to parse"},
					"path": map[string]any{"type": "string", "description": "Dot-notation path (e.g. .data.items[0].name)"},
				},
				"required": []string{"json", "path"},
			},
		},
		{
			Name:        "datetime",
			Description: "Get current UTC time, or compute date math (e.g. +3d, -1h, +30m, -2w).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"operation": map[string]any{"type": "string", "description": "Date math expression (e.g. +3d, -1h). Omit for current time."},
				},
			},
		},
		{
			Name:        "base64_encode",
			Description: "Encode a string to base64.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "String to encode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "base64_decode",
			Description: "Decode a base64 string.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "Base64 string to decode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "url_encode",
			Description: "URL percent-encode a string.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "String to encode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "url_decode",
			Description: "Decode a URL percent-encoded string.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "URL-encoded string to decode"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "hash",
			Description: "Compute a hash of an input string. Supports sha256 and md5.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input":     map[string]any{"type": "string", "description": "String to hash"},
					"algorithm": map[string]any{"type": "string", "description": "Hash algorithm: sha256 (default) or md5"},
				},
				"required": []string{"input"},
			},
		},
		{
			Name:        "math_eval",
			Description: "Evaluate a basic arithmetic expression with +, -, *, /, ^, parentheses, and floats.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"expression": map[string]any{"type": "string", "description": "Arithmetic expression to evaluate"},
				},
				"required": []string{"expression"},
			},
		},
		{
			Name:        "think",
			Description: "A scratchpad tool for organizing your reasoning. Use this to pause and think through your approach before acting. The thought content is the value — the tool simply acknowledges receipt.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thought": map[string]any{"type": "string", "description": "Your internal reasoning, plan, or analysis"},
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
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
}

func (g *GeneralToolsTransport) callWebFetch(ctx context.Context, args map[string]any) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
	}
	rawURL, _ := args["url"].(string)
	if rawURL == "" {
		return errorResult("url is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errorResult(fmt.Sprintf("invalid url: %v", err)), nil
	}

	// Scheme allowlist. Reject file://, ftp://, gopher://, etc. Go's default
	// transport handles only http/https anyway, but we reject explicitly so
	// the error message is informative and no clever transport injection
	// reaches the dialer.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errorResult(fmt.Sprintf("unsupported scheme %q (http/https only)", parsed.Scheme)), nil
	}
	if parsed.Host == "" {
		return errorResult("url missing host"), nil
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

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return errorResult(fmt.Sprintf("invalid request: %v", err)), nil
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errSSRFBlocked) {
			return errorResult(fmt.Sprintf("fetch blocked: %v", err)), nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return errorResult(fmt.Sprintf("cancelled: %v", err)), nil
		}
		return errorResult(fmt.Sprintf("fetch error: %v", err)), nil
	}
	defer resp.Body.Close()

	// Read up to webFetchBodyCap bytes from the upstream. This caps the
	// envelope allocation regardless of what Content-Length the peer sent or
	// whether the peer sent chunked data without a length header.
	limited := io.LimitReader(resp.Body, int64(webFetchBodyCap)+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return errorResult(fmt.Sprintf("read error: %v", err)), nil
	}
	truncatedByBody := len(raw) > webFetchBodyCap
	if truncatedByBody {
		raw = raw[:webFetchBodyCap]
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

	return textResult(out.String()), nil
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
		return errorResult("json and path are required"), nil
	}

	// Parse JSON into any.
	var data any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return errorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	// Navigate the path.
	parts := parseDotPath(pathStr)
	current := data
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part.key]
			if !ok {
				return errorResult(fmt.Sprintf("key %q not found", part.key)), nil
			}
			current = val
		case []any:
			if part.index < 0 || part.index >= len(v) {
				return errorResult(fmt.Sprintf("index %d out of range (length %d)", part.index, len(v))), nil
			}
			current = v[part.index]
		default:
			return errorResult(fmt.Sprintf("cannot navigate into %T at %q", current, part.key)), nil
		}
	}

	// Format the result.
	switch v := current.(type) {
	case string:
		return textResult(v), nil
	case nil:
		return textResult("null"), nil
	default:
		out, _ := json.Marshal(v)
		return textResult(string(out)), nil
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
		return textResult(now.Format(time.RFC3339)), nil
	}

	duration, err := parseDateMath(op)
	if err != nil {
		return errorResult(err.Error()), nil
	}

	result := now.Add(duration)
	return textResult(result.Format(time.RFC3339)), nil
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
		return errorResult("input is required"), nil
	}
	return textResult(base64.StdEncoding.EncodeToString([]byte(input))), nil
}

func (g *GeneralToolsTransport) callBase64Decode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return errorResult(fmt.Sprintf("decode error: %v", err)), nil
	}
	return textResult(string(decoded)), nil
}

func (g *GeneralToolsTransport) callURLEncode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	return textResult(url.QueryEscape(input)), nil
}

func (g *GeneralToolsTransport) callURLDecode(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	decoded, err := url.QueryUnescape(input)
	if err != nil {
		return errorResult(fmt.Sprintf("decode error: %v", err)), nil
	}
	return textResult(decoded), nil
}

func (g *GeneralToolsTransport) callHash(args map[string]any) (*ToolResult, error) {
	input, _ := args["input"].(string)
	if input == "" {
		return errorResult("input is required"), nil
	}
	algorithm, _ := args["algorithm"].(string)
	if algorithm == "" {
		algorithm = "sha256"
	}

	switch algorithm {
	case "sha256":
		h := sha256.Sum256([]byte(input))
		return textResult(hex.EncodeToString(h[:])), nil
	case "md5":
		h := md5.Sum([]byte(input))
		return textResult(hex.EncodeToString(h[:])), nil
	default:
		return errorResult(fmt.Sprintf("unsupported algorithm %q (use sha256 or md5)", algorithm)), nil
	}
}

func (g *GeneralToolsTransport) callMathEval(args map[string]any) (*ToolResult, error) {
	expr, _ := args["expression"].(string)
	if expr == "" {
		return errorResult("expression is required"), nil
	}
	if len(expr) > maxMathExprLen {
		return errorResult(fmt.Sprintf("expression too long: %d bytes (max %d)", len(expr), maxMathExprLen)), nil
	}

	result, err := evalExpr(expr)
	if err != nil {
		return errorResult(fmt.Sprintf("eval error: %v", err)), nil
	}

	// Format nicely: show integer if whole number.
	if result == float64(int64(result)) && !math.IsInf(result, 0) {
		return textResult(strconv.FormatInt(int64(result), 10)), nil
	}
	return textResult(strconv.FormatFloat(result, 'g', -1, 64)), nil
}

func (g *GeneralToolsTransport) callThink(args map[string]any) (*ToolResult, error) {
	raw, exists := args["thought"]
	if !exists || raw == nil {
		return errorResult("thought is required"), nil
	}
	thought, ok := raw.(string)
	if !ok {
		thought = fmt.Sprintf("%v", raw)
	}
	if thought == "" {
		return errorResult("thought is required"), nil
	}
	return textResult("Thought recorded."), nil
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
