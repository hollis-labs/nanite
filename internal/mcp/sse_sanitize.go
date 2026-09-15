package mcp

import (
	"bytes"
	"io"
	"net/url"
	"strings"
)

// sseSanitizeReader adapts a server's SSE stream to what the MCP SDK's client
// assumes, for two things ContextForge does that the SDK does not tolerate.
//
//  1. Non-message events. The SDK's read loop pushes EVERY event's data at the
//     JSON-RPC decoder:
//
//     for evt := range scanEvents(resp.Body) { s.incoming <- evt.Data }
//
//     despite its own doc saying "Reads are SSE 'message' events". ContextForge
//     emits `event: keepalive` with `data: {}` every few seconds, and `{}` is
//     not a JSON-RPC message, so the session dies on the first one with
//     `invalid message version tag ""; expected "2.0"` — during initialize,
//     which makes it look like a handshake failure rather than a keepalive.
//     Events other than `endpoint` and `message` are dropped here.
//
//  2. An endpoint URL with no port. ContextForge advertises
//     `http://contextforge-gateway/servers/<id>/message?session_id=...` —
//     absolute, and missing the :4444 it is actually served on. The SDK resolves
//     it with url.Parse against the SSE URL, and an absolute URL wins outright,
//     so every subsequent POST would go to port 80. The authority is rewritten
//     to the one we connected to; the path and query are the server's to choose.
//
// Both are done at the byte level, in the RoundTripper, because the SDK's
// transports take an *http.Client and expose no other seam. Editing the SDK is
// not ours to do and forking it for this would be worse.
type sseSanitizeReader struct {
	inner io.ReadCloser
	// authority is scheme://host[:port] of the stream we connected to, used to
	// repair an endpoint event that names a different one.
	authority *url.URL

	pending bytes.Buffer // complete, already-sanitized bytes waiting to be read
	partial bytes.Buffer // an event block not yet terminated by a blank line
	err     error
}

func newSSESanitizeReader(inner io.ReadCloser, streamURL *url.URL) *sseSanitizeReader {
	return &sseSanitizeReader{inner: inner, authority: streamURL}
}

func (r *sseSanitizeReader) Read(p []byte) (int, error) {
	for r.pending.Len() == 0 {
		if r.err != nil {
			return 0, r.err
		}
		buf := make([]byte, 4096)
		n, err := r.inner.Read(buf)
		if n > 0 {
			r.partial.Write(buf[:n])
			r.drainBlocks()
		}
		if err != nil {
			r.err = err
			// Flush whatever is left so a final unterminated block is not lost.
			if r.partial.Len() > 0 {
				r.pending.Write(r.partial.Bytes())
				r.partial.Reset()
			}
			if r.pending.Len() == 0 {
				return 0, err
			}
		}
	}
	return r.pending.Read(p)
}

// drainBlocks moves every complete event block from partial to pending,
// dropping or rewriting as needed. An SSE block ends at a blank line.
func (r *sseSanitizeReader) drainBlocks() {
	for {
		data := r.partial.Bytes()
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			return
		}
		block := string(data[:idx+2])
		r.partial.Next(idx + 2)
		if out, keep := r.sanitizeBlock(block); keep {
			r.pending.WriteString(out)
		}
	}
}

// sanitizeBlock decides the fate of one event block.
func (r *sseSanitizeReader) sanitizeBlock(block string) (string, bool) {
	name := ""
	for _, line := range strings.Split(block, "\n") {
		if v, ok := strings.CutPrefix(line, "event:"); ok {
			name = strings.TrimSpace(v)
			break
		}
	}

	switch name {
	case "", "message":
		// The default event type. Keep as-is — this is a JSON-RPC message.
		return block, true
	case "endpoint":
		return r.repairEndpoint(block), true
	default:
		// keepalive, ping, or anything else a server invents. Not JSON-RPC,
		// and the SDK would hand it to the decoder regardless.
		return "", false
	}
}

// repairEndpoint rewrites the endpoint event's data URL onto the authority we
// actually connected to, when the server names a different one.
func (r *sseSanitizeReader) repairEndpoint(block string) string {
	if r.authority == nil {
		return block
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		v, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		raw := strings.TrimSpace(v)
		parsed, err := url.Parse(raw)
		if err != nil || !parsed.IsAbs() {
			// Relative endpoints resolve correctly on their own.
			return block
		}
		if parsed.Host == r.authority.Host {
			return block
		}
		parsed.Scheme = r.authority.Scheme
		parsed.Host = r.authority.Host
		lines[i] = "data: " + parsed.String()
		return strings.Join(lines, "\n")
	}
	return block
}

func (r *sseSanitizeReader) Close() error { return r.inner.Close() }
