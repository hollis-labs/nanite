package apiauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
	_ "modernc.org/sqlite" // registers the sqlite driver for sqlitekit
)

// Store is the credential database. It is a separate SQLite file from
// main.db on purpose: restoring a main.db backup must never resurrect a
// revoked credential.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu       sync.Mutex
	onRevoke []func(credentialID string)
}

// ErrUnauthenticated is returned for any unknown, malformed, revoked or
// expired secret. Callers must not distinguish the reasons to a client.
var ErrUnauthenticated = errors.New("unauthenticated")

const schema = `
CREATE TABLE IF NOT EXISTS credentials (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL CHECK (kind IN ('operator','client','agent')),
	label TEXT NOT NULL,
	scopes TEXT NOT NULL,
	secret_hash TEXT NOT NULL UNIQUE,
	bound_session_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL DEFAULT '',
	last_used_at TEXT NOT NULL DEFAULT '',
	revoked_at TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS ui_sessions (
	id TEXT PRIMARY KEY,
	secret_hash TEXT NOT NULL UNIQUE,
	credential_id TEXT NOT NULL REFERENCES credentials(id),
	csrf TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS login_links (
	id TEXT PRIMARY KEY,
	secret_hash TEXT NOT NULL UNIQUE,
	credential_id TEXT NOT NULL REFERENCES credentials(id),
	expires_at TEXT NOT NULL,
	consumed_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS auth_audit (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	at TEXT NOT NULL,
	credential_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT '',
	method TEXT NOT NULL,
	route TEXT NOT NULL,
	outcome TEXT NOT NULL,
	remote TEXT NOT NULL DEFAULT ''
);
`

// Open opens (creating if needed) the credential database at path.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve auth db path: %w", err)
	}
	db, err := sqlitekit.OpenSingle(ctx, abs, sqlitekit.OpenOptions{Options: sqlitekit.WriterOptions(), CreateParentDir: true})
	if err != nil {
		return nil, fmt.Errorf("open auth db: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init auth db: %w", err)
	}
	s := &Store{db: db, now: time.Now}
	// Audit rows older than 30 days are noise.
	_, _ = db.ExecContext(ctx, `DELETE FROM auth_audit WHERE at < ?`, ts(s.now().Add(-30*24*time.Hour)))
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// OnRevoke registers a callback run (in-process) after a credential is
// revoked. The middleware uses it to cancel that credential's open streams.
func (s *Store) OnRevoke(fn func(credentialID string)) {
	s.mu.Lock()
	s.onRevoke = append(s.onRevoke, fn)
	s.mu.Unlock()
}

func (s *Store) notifyRevoked(id string) {
	s.mu.Lock()
	fns := append([]func(string){}, s.onRevoke...)
	s.mu.Unlock()
	for _, fn := range fns {
		fn(id)
	}
}

// CreateParams describes a new credential.
type CreateParams struct {
	Kind           Kind
	Label          string
	Scopes         []Scope
	BoundSessionID string
	TTL            time.Duration // zero means no expiry
	CreatedBy      string
}

// Create stores a new credential and returns its secret, which is shown once
// and never recoverable. Minting can only narrow: only an operator holds
// admin, a client may not hold admin, and an agent holds tools only.
func (s *Store) Create(ctx context.Context, p CreateParams) (Credential, string, error) {
	scopes := p.Scopes
	switch p.Kind {
	case KindOperator:
		scopes = AllScopes()
	case KindClient:
		if len(scopes) == 0 {
			return Credential{}, "", errors.New("a client credential needs at least one scope")
		}
		for _, sc := range scopes {
			if sc == ScopeAdmin {
				return Credential{}, "", errors.New("a client credential cannot hold the admin scope")
			}
		}
	case KindAgent:
		if p.BoundSessionID == "" {
			return Credential{}, "", errors.New("an agent credential must be bound to a session")
		}
		scopes = []Scope{ScopeTools}
	default:
		return Credential{}, "", fmt.Errorf("unknown credential kind %q", p.Kind)
	}
	if p.Label == "" {
		return Credential{}, "", errors.New("a credential needs a label")
	}
	secret, hash, err := newSecret(p.Kind)
	if err != nil {
		return Credential{}, "", err
	}
	now := s.now().UTC()
	c := Credential{ID: uuid.NewString(), Kind: p.Kind, Label: p.Label, Scopes: scopes, BoundSessionID: p.BoundSessionID, CreatedAt: now, CreatedBy: p.CreatedBy}
	if p.TTL > 0 {
		c.ExpiresAt = now.Add(p.TTL)
	}
	scopesJSON, _ := json.Marshal(scopes)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO credentials(id,kind,label,scopes,secret_hash,bound_session_id,created_at,expires_at,created_by) VALUES(?,?,?,?,?,?,?,?,?)`,
		c.ID, string(c.Kind), c.Label, string(scopesJSON), hash, c.BoundSessionID, ts(c.CreatedAt), ts(c.ExpiresAt), c.CreatedBy); err != nil {
		return Credential{}, "", fmt.Errorf("store credential: %w", err)
	}
	return c, secret, nil
}

// ImportSecret registers an externally supplied operator secret (the
// NANITE_AUTH_TOKEN override for headless runs). It must have the operator
// shape. Idempotent: returns the existing credential when already stored.
func (s *Store) ImportSecret(ctx context.Context, secret, label string) (Credential, error) {
	kind, err := kindOfSecret(secret)
	if err != nil || kind != KindOperator {
		return Credential{}, errors.New("NANITE_AUTH_TOKEN must be an operator credential (nan_operator_...)")
	}
	hash := hashSecret(secret)
	if c, err := s.byHash(ctx, hash); err == nil {
		return c, nil
	}
	now := s.now().UTC()
	c := Credential{ID: uuid.NewString(), Kind: KindOperator, Label: label, Scopes: AllScopes(), CreatedAt: now, CreatedBy: "env"}
	scopesJSON, _ := json.Marshal(c.Scopes)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO credentials(id,kind,label,scopes,secret_hash,created_at,created_by) VALUES(?,?,?,?,?,?,?)`,
		c.ID, string(c.Kind), c.Label, string(scopesJSON), hash, ts(c.CreatedAt), c.CreatedBy); err != nil {
		return Credential{}, fmt.Errorf("store credential: %w", err)
	}
	return c, nil
}

const selectColumns = `id,kind,label,scopes,bound_session_id,created_at,expires_at,last_used_at,revoked_at,created_by`

func scanCredential(row interface{ Scan(...any) error }) (Credential, error) {
	var c Credential
	var kind, scopes, created, expires, used, revoked string
	if err := row.Scan(&c.ID, &kind, &c.Label, &scopes, &c.BoundSessionID, &created, &expires, &used, &revoked, &c.CreatedBy); err != nil {
		return Credential{}, err
	}
	c.Kind = Kind(kind)
	var names []string
	_ = json.Unmarshal([]byte(scopes), &names)
	for _, n := range names {
		c.Scopes = append(c.Scopes, Scope(n))
	}
	c.CreatedAt, c.ExpiresAt, c.LastUsedAt, c.RevokedAt = parseTS(created), parseTS(expires), parseTS(used), parseTS(revoked)
	return c, nil
}

func (s *Store) byHash(ctx context.Context, hash string) (Credential, error) {
	return scanCredential(s.db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM credentials WHERE secret_hash=?`, hash))
}

// Authenticate resolves a presented secret. Every failure is
// ErrUnauthenticated: callers must not tell a client why.
func (s *Store) Authenticate(ctx context.Context, secret string) (Credential, error) {
	if _, err := kindOfSecret(secret); err != nil {
		return Credential{}, ErrUnauthenticated
	}
	c, err := s.byHash(ctx, hashSecret(secret))
	if err != nil {
		return Credential{}, ErrUnauthenticated
	}
	now := s.now().UTC()
	if !c.Active(now) {
		return Credential{}, ErrUnauthenticated
	}
	// last_used is advisory: update at most every 30 seconds.
	if now.Sub(c.LastUsedAt) > 30*time.Second {
		_, _ = s.db.ExecContext(ctx, `UPDATE credentials SET last_used_at=? WHERE id=?`, ts(now), c.ID)
		c.LastUsedAt = now
	}
	return c, nil
}

// Get returns one credential by id.
func (s *Store) Get(ctx context.Context, id string) (Credential, error) {
	return scanCredential(s.db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM credentials WHERE id=?`, id))
}

// List returns credentials, newest first, optionally including revoked ones.
func (s *Store) List(ctx context.Context, includeRevoked bool) ([]Credential, error) {
	q := `SELECT ` + selectColumns + ` FROM credentials`
	if !includeRevoked {
		q += ` WHERE revoked_at=''`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// HasActiveOperator reports whether any non-revoked, unexpired operator
// credential exists.
func (s *Store) HasActiveOperator(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM credentials WHERE kind='operator' AND revoked_at=''`).Scan(&n)
	return n > 0, err
}

// Revoke revokes a credential and every UI session and link that depends on
// it, then notifies in-process listeners so open streams close. It reports
// whether a live credential was revoked.
func (s *Store) Revoke(ctx context.Context, id string) (bool, error) {
	now := ts(s.now())
	res, err := s.db.ExecContext(ctx, `UPDATE credentials SET revoked_at=? WHERE id=? AND revoked_at=''`, now, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE ui_sessions SET revoked_at=? WHERE credential_id=? AND revoked_at=''`, now, id); err != nil {
		return true, err
	}
	s.notifyRevoked(id)
	return true, nil
}

// RevokeSessionCredentials revokes every agent credential bound to a session
// (used when the session ends).
func (s *Store) RevokeSessionCredentials(ctx context.Context, sessionID string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM credentials WHERE kind='agent' AND bound_session_id=? AND revoked_at=''`, sessionID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		if _, err := s.Revoke(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Slide extends an agent credential's expiry to now+ttl, never beyond
// createdAt+maxLife. It is how a live session keeps its token.
func (s *Store) Slide(ctx context.Context, c Credential, ttl, maxLife time.Duration) (time.Time, error) {
	now := s.now().UTC()
	next := now.Add(ttl)
	if limit := c.CreatedAt.Add(maxLife); next.After(limit) {
		next = limit
	}
	if !next.After(c.ExpiresAt) {
		return c.ExpiresAt, nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE credentials SET expires_at=? WHERE id=? AND revoked_at=''`, ts(next), c.ID); err != nil {
		return c.ExpiresAt, err
	}
	return next, nil
}

// Rotate replaces a credential with a fresh one of the same kind, label and
// scopes, revoking the old one. The new secret is returned once.
func (s *Store) Rotate(ctx context.Context, id, createdBy string) (Credential, string, error) {
	old, err := s.Get(ctx, id)
	if err != nil {
		return Credential{}, "", fmt.Errorf("credential not found")
	}
	var ttl time.Duration
	if !old.ExpiresAt.IsZero() {
		ttl = old.ExpiresAt.Sub(old.CreatedAt)
	}
	fresh, secret, err := s.Create(ctx, CreateParams{Kind: old.Kind, Label: old.Label, Scopes: old.Scopes, BoundSessionID: old.BoundSessionID, TTL: ttl, CreatedBy: createdBy})
	if err != nil {
		return Credential{}, "", err
	}
	if _, err := s.Revoke(ctx, id); err != nil {
		return Credential{}, "", err
	}
	return fresh, secret, nil
}

// Audit records one event. It never records a secret.
func (s *Store) Audit(ctx context.Context, credentialID string, kind Kind, method, route, outcome, remote string) {
	_, _ = s.db.ExecContext(ctx, `INSERT INTO auth_audit(at,credential_id,kind,method,route,outcome,remote) VALUES(?,?,?,?,?,?,?)`,
		ts(s.now()), credentialID, string(kind), method, route, outcome, remote)
}

// UISession is a browser session established by login.
type UISession struct {
	ID           string
	CredentialID string
	CSRF         string
	ExpiresAt    time.Time
}

// UI session lifetime: 12 hours, sliding.
const uiSessionTTL = 12 * time.Hour

// CreateUISession starts a browser session for an operator or client
// credential and returns the cookie secret (shown once) and the CSRF token.
func (s *Store) CreateUISession(ctx context.Context, c Credential) (UISession, string, error) {
	if c.Kind == KindAgent {
		return UISession{}, "", errors.New("agent credentials cannot start a UI session")
	}
	cookie, hash, err := opaqueToken()
	if err != nil {
		return UISession{}, "", err
	}
	csrf, _, err := opaqueToken()
	if err != nil {
		return UISession{}, "", err
	}
	now := s.now().UTC()
	u := UISession{ID: uuid.NewString(), CredentialID: c.ID, CSRF: csrf, ExpiresAt: now.Add(uiSessionTTL)}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO ui_sessions(id,secret_hash,credential_id,csrf,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		u.ID, hash, c.ID, csrf, ts(now), ts(u.ExpiresAt)); err != nil {
		return UISession{}, "", err
	}
	return u, cookie, nil
}

// AuthenticateUISession resolves a cookie secret to its session and
// credential, sliding the session expiry.
func (s *Store) AuthenticateUISession(ctx context.Context, cookie string) (UISession, Credential, error) {
	if cookie == "" {
		return UISession{}, Credential{}, ErrUnauthenticated
	}
	var u UISession
	var expires, revoked string
	err := s.db.QueryRowContext(ctx, `SELECT id,credential_id,csrf,expires_at,revoked_at FROM ui_sessions WHERE secret_hash=?`, hashSecret(cookie)).
		Scan(&u.ID, &u.CredentialID, &u.CSRF, &expires, &revoked)
	if err != nil {
		return UISession{}, Credential{}, ErrUnauthenticated
	}
	u.ExpiresAt = parseTS(expires)
	now := s.now().UTC()
	if revoked != "" || !now.Before(u.ExpiresAt) {
		return UISession{}, Credential{}, ErrUnauthenticated
	}
	c, err := s.Get(ctx, u.CredentialID)
	if err != nil || !c.Active(now) {
		return UISession{}, Credential{}, ErrUnauthenticated
	}
	if next := now.Add(uiSessionTTL); next.Sub(u.ExpiresAt) > time.Minute {
		_, _ = s.db.ExecContext(ctx, `UPDATE ui_sessions SET expires_at=? WHERE id=?`, ts(next), u.ID)
		u.ExpiresAt = next
	}
	return u, c, nil
}

// DeleteUISession ends a browser session.
func (s *Store) DeleteUISession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ui_sessions SET revoked_at=? WHERE id=? AND revoked_at=''`, ts(s.now()), id)
	return err
}

// loginLinkTTL is how long a one-time login link lives.
const loginLinkTTL = 60 * time.Second

// CreateLoginLink mints a single-use code that logs a browser in as the
// credential. Only an operator credential may be linked.
func (s *Store) CreateLoginLink(ctx context.Context, c Credential) (string, time.Time, error) {
	if c.Kind != KindOperator {
		return "", time.Time{}, errors.New("only an operator credential can mint a login link")
	}
	code, hash, err := opaqueToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().UTC().Add(loginLinkTTL)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO login_links(id,secret_hash,credential_id,expires_at) VALUES(?,?,?,?)`, uuid.NewString(), hash, c.ID, ts(expires)); err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// ConsumeLoginLink atomically spends a one-time code and returns the
// credential it belongs to.
func (s *Store) ConsumeLoginLink(ctx context.Context, code string) (Credential, error) {
	now := s.now().UTC()
	var credID string
	err := s.db.QueryRowContext(ctx, `UPDATE login_links SET consumed_at=? WHERE secret_hash=? AND consumed_at='' AND expires_at>? RETURNING credential_id`,
		ts(now), hashSecret(code), ts(now)).Scan(&credID)
	if err != nil {
		return Credential{}, ErrUnauthenticated
	}
	c, err := s.Get(ctx, credID)
	if err != nil || !c.Active(now) {
		return Credential{}, ErrUnauthenticated
	}
	return c, nil
}
