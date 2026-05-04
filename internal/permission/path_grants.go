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
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// lineageMaxHops bounds how many parent links LookupPath will walk before
// giving up. Workers are typically one hop deep; this cap protects against
// pathological cycles that should never exist in production but would
// otherwise spin LookupPath if the lineage map were ever corrupted.
const lineageMaxHops = 4

// PathGrants tracks session-scoped, explicit-mention path grants.
type PathGrants struct {
	mu sync.RWMutex
	// grants[sessionID] = set of cleaned absolute paths granted for this
	// session. The set is represented as a map[string]struct{} so the
	// IsPathAllowed prefix check can iterate without sorting.
	grants map[string]map[string]struct{}
	// lineage[childSessionID] = parentSessionID. Stamped at worker spawn
	// time by the dispatch wiring layer. Lets LookupPath fall through to
	// the parent's grant bucket when the worker's own bucket misses, so
	// explicit-mention grants the user issued in the chat thread can be
	// honored by spawned workers without making profile permissions
	// inheritable. Cleared at worker spawn-finish (defer in the runner).
	lineage map[string]string
}

// NewPathGrants returns an empty grant store.
func NewPathGrants() *PathGrants {
	return &PathGrants{
		grants:  make(map[string]map[string]struct{}),
		lineage: make(map[string]string),
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

// LookupKind classifies how a candidate matched the session's grant
// bucket. "none" — no match. "literal" — exact-path hit on a granted
// entry. "ancestor" — granted root is a directory ancestor of the
// candidate. Used by callers (notably the dev_tools observability log)
// that need to distinguish miss reasons; IsPathAllowed wraps it for
// callers that only care about the boolean.
type LookupKind string

const (
	LookupKindNone            LookupKind = "none"
	LookupKindLiteral         LookupKind = "literal"
	LookupKindAncestor        LookupKind = "ancestor"
	LookupKindAncestorSession LookupKind = "ancestor_session"
)

// LookupPath reports whether sessionID has been granted access to
// candidate, and classifies the match kind for diagnostic surfaces.
// The check accepts the literal cleaned-absolute candidate AND any
// granted root that is an ancestor of the candidate. This matches the
// Q2 promise: a grant for "/foo/bar.go" implies the literal file plus
// its parent directory "/foo/" — and a tool call against "/foo/anything"
// hits the parent grant.
//
// On a miss against the session's own bucket, LookupPath walks the
// session lineage chain (RegisterLineage) up to lineageMaxHops and
// retries the same literal/ancestor check against each parent's bucket.
// A hit via lineage returns kind=LookupKindAncestorSession and the
// matching parent's session ID in viaSessionID. This is how a spawned
// worker session resolves grants the user explicitly issued in the
// parent chat thread, without making profile permissions inheritable.
//
// Returns (false, LookupKindNone, "") when sessionID is empty, candidate
// is empty, neither the session's own bucket nor any walked ancestor
// matches.
func (g *PathGrants) LookupPath(sessionID, candidate string) (bool, LookupKind, string) {
	if g == nil || sessionID == "" || candidate == "" {
		return false, LookupKindNone, ""
	}
	abs, ok := absolutize(candidate)
	if !ok {
		return false, LookupKindNone, ""
	}

	// Hold the read lock only for the bucket + lineage walk; the warn-level
	// log on a depth-cap hit fires AFTER the lock is released so it can't
	// stall sibling readers if the slog handler blocks. The IIFE bounds the
	// lock scope; walkCapped escapes via the closure capture.
	var walkCapped bool
	matched, kind, via := func() (bool, LookupKind, string) {
		g.mu.RLock()
		defer g.mu.RUnlock()

		// Own bucket first — keep the existing literal/ancestor classification
		// so callers that already grant against their own session see the same
		// kind value as before this change.
		if m, k := lookupInBucket(g.grants[sessionID], abs); m {
			return true, k, ""
		}

		// Lineage walk. Re-classify any hit against an ancestor bucket as
		// ancestor_session so callers can distinguish "this session's own
		// grant" from "inherited via parent chain" in the diagnostic log.
		visited := map[string]struct{}{sessionID: {}}
		cursor := sessionID
		for hop := 0; hop < lineageMaxHops; hop++ {
			parent, ok := g.lineage[cursor]
			if !ok || parent == "" {
				return false, LookupKindNone, ""
			}
			if _, dup := visited[parent]; dup {
				// Cycle: the lineage map should be acyclic (worker → chat
				// is a one-shot pointer cleared on spawn-finish), but bail
				// rather than spin if it ever isn't.
				return false, LookupKindNone, ""
			}
			visited[parent] = struct{}{}
			if m, _ := lookupInBucket(g.grants[parent], abs); m {
				return true, LookupKindAncestorSession, parent
			}
			cursor = parent
		}
		// Walk hit the depth cap. Signal to the caller; the slog.Warn fires
		// outside the lock to avoid blocking sibling readers.
		walkCapped = true
		return false, LookupKindNone, ""
	}()

	if walkCapped {
		// Production lineage chains should be one or two hops; logging at
		// warn surfaces a misuse without failing the tool call (the dev_*
		// call reports a clean miss).
		slog.Warn("permission: path-grant lineage walk capped",
			"session_id", sessionID,
			"candidate_abs", abs,
			"max_hops", lineageMaxHops,
		)
	}
	return matched, kind, via
}

// lookupInBucket runs the existing literal + ancestor check against a
// single session's grant bucket. Returns (false, LookupKindNone) on an
// empty bucket so callers can chain it across the lineage walk without
// duplicating the empty-check.
func lookupInBucket(bucket map[string]struct{}, abs string) (bool, LookupKind) {
	if len(bucket) == 0 {
		return false, LookupKindNone
	}
	if _, ok := bucket[abs]; ok {
		return true, LookupKindLiteral
	}
	for granted := range bucket {
		if granted == "" {
			continue
		}
		if abs == granted {
			return true, LookupKindLiteral
		}
		if strings.HasPrefix(abs, ensureTrailingSep(granted)) {
			return true, LookupKindAncestor
		}
	}
	return false, LookupKindNone
}

// IsPathAllowed reports whether sessionID has been granted access to
// candidate. Wraps LookupPath for callers that only need the boolean.
func (g *PathGrants) IsPathAllowed(sessionID, candidate string) bool {
	matched, _, _ := g.LookupPath(sessionID, candidate)
	return matched
}

// RegisterLineage records that childSessionID is a spawned descendant of
// parentSessionID for the purpose of grant lookup. After this call,
// LookupPath(childSessionID, candidate) will fall through to
// parentSessionID's bucket on a miss against the child's own bucket.
//
// Nil-safe; no-op when either ID is empty. Replacing an existing entry
// is allowed (last-writer-wins) — production callers use a fresh worker
// session ID per spawn so the second-write case is theoretical.
//
// Cleared by ClearLineage at spawn-finish so the entry's lifetime is
// exactly the worker's lifetime.
func (g *PathGrants) RegisterLineage(childSessionID, parentSessionID string) {
	if g == nil || childSessionID == "" || parentSessionID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lineage[childSessionID] = parentSessionID
}

// ClearLineage drops the lineage entry for sessionID. Nil-safe. Called
// via defer at worker spawn-finish so the entry is cleaned up even if
// the spawn errors out.
func (g *PathGrants) ClearLineage(sessionID string) {
	if g == nil || sessionID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.lineage, sessionID)
}

// BucketSize returns the number of grants registered for sessionID. Zero
// when the session has no bucket or the store is nil. Used by diagnostic
// surfaces (notably the dev_tools resolution log) to distinguish a
// "no bucket" miss from a "bucket exists but no entry matched" miss.
func (g *PathGrants) BucketSize(sessionID string) int {
	if g == nil || sessionID == "" {
		return 0
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.grants[sessionID])
}

// BestSessionDir returns the longest grant for sessionID that exists on
// disk as a directory, or empty string when no such grant exists.
//
// Used by dev_bash to derive a default working_dir when the agent omits
// it, so the path-grant gate runs uniformly across dev_* tools (no
// silent sandbox-only bypass). Specificity is encoded as path length:
// the literal user-mentioned path is longer than its registered parent,
// so a mention like ~/foo/bar.go yields the file's parent directory
// (the bucket also contains the parent registration). Returning the
// longest existing-directory match is the most-specific dir the user
// has signalled intent toward.
//
// Order rationale: registration always co-stamps the literal AND its
// parent (path_grants.go RegisterFromUserMessage). Sorting by length
// descending therefore prefers the most specific path first. The
// IsDir() filter falls through to the parent automatically when the
// literal itself is a file.
//
// CW-20260504-0003: walks the lineage chain (depth-bounded by
// lineageMaxHops) so a spawned worker session whose own bucket is
// empty inherits the parent chat session's most-specific default
// directory. Mirrors the LookupPath lineage walk so workers can default
// their dev_bash cwd to the path the user originally granted in the
// parent thread.
func (g *PathGrants) BestSessionDir(sessionID string) string {
	if g == nil || sessionID == "" {
		return ""
	}

	g.mu.RLock()
	candidates := make([]string, 0)
	for p := range g.grants[sessionID] {
		candidates = append(candidates, p)
	}
	visited := map[string]struct{}{sessionID: {}}
	cursor := sessionID
	for hop := 0; hop < lineageMaxHops; hop++ {
		parent, ok := g.lineage[cursor]
		if !ok || parent == "" {
			break
		}
		if _, dup := visited[parent]; dup {
			break // defensive cycle guard
		}
		visited[parent] = struct{}{}
		for p := range g.grants[parent] {
			candidates = append(candidates, p)
		}
		cursor = parent
	}
	g.mu.RUnlock()

	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return len(candidates[i]) > len(candidates[j])
	})
	// Dedupe in-place while preserving sort order — a literal granted by
	// both the worker and the parent (rare, but possible) shouldn't be
	// stat'd twice.
	seen := make(map[string]struct{}, len(candidates))
	for _, p := range candidates {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		info, err := os.Stat(p)
		if err == nil && info.IsDir() {
			return p
		}
	}
	return ""
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
//
// Home resolution goes through HomeDir (not os.UserHomeDir) so the
// path-grant store stays consistent with dev_tools.expandHome on
// launchd-spawned services where $HOME is unset (CW-20260502-0014).
func absolutize(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		home, err := HomeDir()
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
