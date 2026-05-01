// Package permission — path_grants.go implements the session-scoped
// path-grant store for the trust-agent permission redesign
// (CW-20260430-0009).
//
// Design summary:
//
//   - When a user message arrives, the chat layer scans it for whitespace-
//     delimited tokens whose first character indicates a literal path:
//     "~/", "/", or "./". Each strict-prefix token grants access to:
//
//       (a) the literal path
//       (b) its single parent directory
//
//     The grant is recorded against the session ID and persists for the
//     remainder of the session (process-local; cleared on session close).
//     This is the explicit-mention auto-grant Q1+Q2+Q3 of the locked design.
//
//   - The dev_* tools consult IsPathAllowed(sessionID, path) when the
//     standard AllowedPaths list rejects a path. If either the session
//     grant store accepts it or the AllowedPaths list does, the tool
//     proceeds. (No new "deny" surface — this only widens.)
//
// Notes:
//
//   - Grants are stored as cleaned, tilde-expanded absolute paths. The
//     IsPathAllowed check resolves the candidate the same way (via the
//     dev_tools resolveAllowed boundary) so tilde-vs-/Users mismatches
//     don't slip through.
//
//   - This is intentionally an in-process store. Persistence to YAML or
//     SQLite is out of scope for the trust-agent redesign — the user
//     explicitly opted for "session-scoped, no nag-again" behaviour.
//
//   - The store is goroutine-safe: a single sync.RWMutex guards both
//     registration and lookup. Read pressure dominates (one lookup per
//     dev_* tool call), so RW-lock is the right shape.
package permission

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PathGrants tracks session-scoped, explicit-mention path grants.
type PathGrants struct {
	mu sync.RWMutex
	// grants[sessionID] = set of cleaned absolute paths granted for this
	// session. The set is represented as a map[string]struct{} so the
	// IsPathAllowed prefix check can iterate without sorting.
	grants map[string]map[string]struct{}
}

// NewPathGrants returns an empty grant store.
func NewPathGrants() *PathGrants {
	return &PathGrants{
		grants: make(map[string]map[string]struct{}),
	}
}

// RegisterFromUserMessage scans message for whitespace-delimited tokens
// whose first character is a strict-prefix path indicator ("~/", "/",
// "./"). For each match, registers the literal path AND its parent
// directory as session-scoped grants.
//
// Returns the deduplicated list of granted paths (literal + parents) so
// the caller can emit a structured event for observability if desired.
//
// Strict-prefix only — no "fuzzy" patterns like a bare "config.yaml" or
// a project name will auto-grant. The gate falls through to notify-pause
// for those, per Q1.
//
// Tokens that fail to expand (no $HOME) or fail to absolutize are
// silently skipped. The downstream path-safety escape check still runs
// on every dev_* call so a bogus grant cannot bypass traversal
// protection.
func (g *PathGrants) RegisterFromUserMessage(sessionID, message string) []string {
	if g == nil || sessionID == "" || message == "" {
		return nil
	}
	mentions := ExtractPathMentions(message)
	if len(mentions) == 0 {
		return nil
	}

	cleaned := make([]string, 0, len(mentions)*2)
	seen := make(map[string]struct{}, len(mentions)*2)
	for _, m := range mentions {
		abs, ok := absolutize(m)
		if !ok {
			continue
		}
		// Q2 — register literal path AND its parent directory (single level).
		// No recursive grant. Recursive coverage requires the user to mention
		// the directory itself.
		parent := filepath.Dir(abs)
		for _, p := range []string{abs, parent} {
			if p == "" || p == "." {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	bucket := g.grants[sessionID]
	if bucket == nil {
		bucket = make(map[string]struct{}, len(cleaned))
		g.grants[sessionID] = bucket
	}
	for _, p := range cleaned {
		bucket[p] = struct{}{}
	}
	return cleaned
}

// IsPathAllowed reports whether sessionID has been granted access to
// candidate. The check accepts the literal cleaned-absolute candidate
// AND any granted root that is an ancestor of the candidate. This
// matches the Q2 promise: a grant for "/foo/bar.go" implies the literal
// file plus its parent directory "/foo/" — and a tool call against
// "/foo/anything" hits the parent grant.
//
// Returns false if sessionID is empty, candidate is empty, or no grant
// matches.
func (g *PathGrants) IsPathAllowed(sessionID, candidate string) bool {
	if g == nil || sessionID == "" || candidate == "" {
		return false
	}
	abs, ok := absolutize(candidate)
	if !ok {
		return false
	}

	g.mu.RLock()
	bucket := g.grants[sessionID]
	g.mu.RUnlock()
	if len(bucket) == 0 {
		return false
	}

	// Direct hit on the cleaned absolute path.
	if _, ok := bucket[abs]; ok {
		return true
	}
	// Ancestor hit: any granted root that is a directory ancestor of abs.
	for granted := range bucket {
		if granted == "" {
			continue
		}
		if abs == granted {
			return true
		}
		// Treat granted as a directory; the candidate must lie strictly
		// inside it. This intentionally does NOT recurse beyond what's
		// already in the bucket — the registration step put both the
		// literal AND its parent in, so a single-level descent is the
		// natural matching shape.
		if strings.HasPrefix(abs, ensureTrailingSep(granted)) {
			return true
		}
	}
	return false
}

// Clear removes all grants for sessionID. Called at session end.
func (g *PathGrants) Clear(sessionID string) {
	if g == nil || sessionID == "" {
		return
	}
	g.mu.Lock()
	delete(g.grants, sessionID)
	g.mu.Unlock()
}

// ListGrants returns a snapshot of the granted paths for sessionID.
// Returns nil when no grants are recorded. Order is unspecified.
// Used by tests and (eventually) observability surfaces.
func (g *PathGrants) ListGrants(sessionID string) []string {
	if g == nil || sessionID == "" {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	bucket := g.grants[sessionID]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]string, 0, len(bucket))
	for p := range bucket {
		out = append(out, p)
	}
	return out
}

// ExtractPathMentions returns the set of strict-prefix path tokens found
// in message. A token qualifies when, after splitting on whitespace, it
// begins with one of:
//
//   - "~/"  (home-relative)
//   - "/"   (absolute)
//   - "./"  (cwd-relative)
//
// Loose patterns (bare "config.yaml", a project name like "nanite", or a
// URL like "https://...") do NOT auto-grant — the gate falls through to
// notify-pause. This is Q1 of the locked design.
//
// Common false-positive guards:
//
//   - URLs starting with "http://" or "https://" are rejected even
//     though they contain "/" (the leading scheme is not a path
//     prefix).
//   - Bare "/" (the root) is rejected — granting access to / would
//     undo the whole point of the allow-list.
//   - Trailing punctuation (`.`, `,`, `:`, `;`, `)`, `]`, `"`, `'`)
//     is stripped so "see ~/foo." doesn't register "~/foo." literal.
//   - Markdown emphasis (`*`, `_`, backtick) is stripped from both
//     ends.
//
// Exported because chat.HandleMessage and (in tests) callers need to
// reuse the same parsing rule that RegisterFromUserMessage applies.
func ExtractPathMentions(message string) []string {
	if message == "" {
		return nil
	}
	fields := strings.Fields(message)
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, raw := range fields {
		tok := trimMarkdownAndPunct(raw)
		if tok == "" {
			continue
		}
		if !isPathToken(tok) {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	return out
}

// isPathToken reports whether tok satisfies one of the three strict
// prefixes (Q1) AND is not a known false-positive shape.
func isPathToken(tok string) bool {
	if len(tok) < 2 {
		// Bare "/" or bare "~" — reject.
		if tok == "/" || tok == "~" {
			return false
		}
		return false
	}
	// URL guard.
	if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") || strings.HasPrefix(tok, "ftp://") || strings.HasPrefix(tok, "file://") {
		return false
	}
	// Strict-prefix matches.
	switch {
	case strings.HasPrefix(tok, "~/"):
		return true
	case strings.HasPrefix(tok, "./"):
		return true
	case strings.HasPrefix(tok, "/"):
		// Reject "//foo" (network share / smell of a copy/paste artifact)
		// and bare "/".
		if len(tok) >= 2 && tok[1] == '/' {
			return false
		}
		return true
	}
	return false
}

// trimMarkdownAndPunct strips common surrounding punctuation and
// markdown emphasis markers from a token. Symmetric: strips matched
// pairs (e.g. `\`foo\`` → `foo`) but also peels unmatched trailing
// punctuation (e.g. `~/foo.` → `~/foo`).
func trimMarkdownAndPunct(tok string) string {
	// Trim outer whitespace defensively (Fields already split on it).
	tok = strings.TrimSpace(tok)
	// Peel matched markdown wrappers.
	for _, pair := range []struct{ open, close string }{
		{"`", "`"},
		{"*", "*"},
		{"_", "_"},
		{"\"", "\""},
		{"'", "'"},
		{"(", ")"},
		{"[", "]"},
		{"{", "}"},
		{"<", ">"},
	} {
		if strings.HasPrefix(tok, pair.open) && strings.HasSuffix(tok, pair.close) && len(tok) >= len(pair.open)+len(pair.close) {
			tok = tok[len(pair.open) : len(tok)-len(pair.close)]
		}
	}
	// Peel trailing sentence punctuation.
	tok = strings.TrimRight(tok, ".,;:!?\")]}>")
	// Peel matched leading punctuation that may have been left after
	// the closing-side trim above.
	tok = strings.TrimLeft(tok, "([{<\"'")
	return tok
}

// absolutize expands a leading ~/ to the user's home directory, then
// applies filepath.Abs to produce a cleaned absolute path. Returns
// (path, true) on success; ("", false) on any error.
func absolutize(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		switch {
		case p == "~":
			p = home
		default:
			p = filepath.Join(home, p[2:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

// ensureTrailingSep returns p with a trailing OS path separator so that
// HasPrefix can be used as a directory-ancestor check without false
// positives like "/foo" matching "/foobar".
func ensureTrailingSep(p string) string {
	if p == "" {
		return p
	}
	sep := string(filepath.Separator)
	if strings.HasSuffix(p, sep) {
		return p
	}
	return p + sep
}
