// Package providerkeys resolves the API key of a native provider by reference.
//
// A provider's key is not a value Nanite holds. It is a reference
// (keychain://, helper://, file://, op://) that is resolved when the provider
// is first used, through libs/go-secrets. Three places can answer, in order:
//
//  1. the provider's own key_ref, if its settings carry one;
//  2. the default keychain entry keychain://nanite/provider/<id>, which also
//     reads the pre-reference location (service "nanite", account
//     "provider-api-key:<id>") so an existing install keeps working;
//  3. the conventional environment variable (ANTHROPIC_API_KEY), only when the
//     operator has enabled it with NANITE_PROVIDER_KEY_ENV=1.
//
// Every answer reports where it came from. A key that cannot be found is a
// typed error (not found, denied, unavailable), never an empty string.
package providerkeys

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	secrets "github.com/hollis-labs/go-secrets"
	"github.com/hollis-labs/go-secrets/redact"
	"github.com/hollis-labs/nanite/internal/brand"
	legacykeys "github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/store"
)

const (
	// EnvEnableVar turns on the environment fallback (step 3). Without it a
	// provider key is never read from the process environment.
	EnvEnableVar = "NANITE_PROVIDER_KEY_ENV"

	// SettingsKey is the key_ref field in a provider row's settings JSON.
	SettingsKey = "key_ref"

	defaultPrefix = "provider/"
)

// DefaultRef is the keychain reference a provider's key is saved under when no
// key_ref is configured.
func DefaultRef(providerID string) string {
	return "keychain://" + brand.ID + "/" + defaultPrefix + providerID
}

// legacyAccount maps a default reference back to the account the key had
// before references existed. Any other reference has no legacy location.
func legacyAccount(ref secrets.Ref) string {
	if ref.Authority != brand.ID || len(ref.Path) != 2 || ref.Path[0] != strings.TrimSuffix(defaultPrefix, "/") {
		return ""
	}
	return legacykeys.ProviderKeyName(ref.Path[1])
}

// Settings is the part of the provider store the manager reads and writes.
type Settings interface {
	GetProvider(ctx context.Context, id string) (*store.ProviderConfig, error)
	UpdateProvider(ctx context.Context, id string, u store.ProviderUpdate) error
}

// Options configure a Manager.
type Options struct {
	// Settings holds each provider's key_ref. Nil means no provider has one.
	Settings Settings
	// EnvNames returns the conventional environment variable for a provider
	// row id ("" for none).
	EnvNames func(providerID string) string
	// EnvEnabled opts in to step 3. FromEnvironment reads EnvEnableVar.
	EnvEnabled bool
	// Store replaces the OS credential store behind keychain://. Tests pass
	// secretstest.FakeStore; nil uses the OS keychain.
	Store secrets.Store
	// Resolver replaces the default resolver; tests pass one built over
	// secretstest. The manager still applies its own env and redaction policy
	// only to a resolver it builds itself.
	Resolver *secrets.Resolver
	// Redact learns every key the manager resolves. Nil uses Redactor().
	Redact *redact.Set
	// LookupEnv replaces os.LookupEnv for step 3 and the disabled-env hint.
	LookupEnv func(string) (string, bool)
	// DeleteLegacy removes a pre-reference keychain account of Nanite's own.
	// Nil uses the OS keychain; tests replace it.
	DeleteLegacy func(account string) error
}

// Manager resolves, reports on and writes provider keys.
type Manager struct {
	res       *secrets.Resolver
	settings  Settings
	envNames  func(string) string
	envOn     bool
	lookup    func(string) (string, bool)
	delLegacy func(string) error
}

var sharedRedactor = &redact.Set{}

// Redactor is the process-wide set of key values the manager has resolved. The
// log handler consults it so a key cannot be written to a log even by a call
// site that does not know it holds one.
func Redactor() *redact.Set { return sharedRedactor }

// FromEnvironment reports whether the operator enabled the environment
// fallback.
func FromEnvironment() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvEnableVar))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// New returns a Manager.
func New(o Options) *Manager {
	if o.Redact == nil {
		o.Redact = sharedRedactor
	}
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.DeleteLegacy == nil {
		o.DeleteLegacy = legacykeys.DeleteChecked
	}
	res := o.Resolver
	if res == nil {
		opts := []secrets.Option{
			secrets.WithLegacy(brand.ID, legacyAccount),
			secrets.WithRedactor(o.Redact),
			secrets.WithEnvLookup(o.LookupEnv),
			secrets.WithAudit(func(ev secrets.Event) {
				// Names only: the event never carries a value.
				slog.Debug("provider key resolution", "ref", ev.Ref, "source", ev.Source, "error_kind", string(ev.Kind))
			}),
		}
		if o.Store != nil {
			opts = append(opts, secrets.WithStore(o.Store))
		}
		if o.EnvEnabled {
			opts = append(opts, secrets.WithEnv())
		}
		res = secrets.New(opts...)
	}
	return &Manager{res: res, settings: o.Settings, envNames: o.EnvNames, envOn: o.EnvEnabled, lookup: o.LookupEnv, delLegacy: o.DeleteLegacy}
}

// Report says where a resolved key came from. It never carries the key.
type Report struct {
	Ref    string
	Source string
}

// Error is returned when no step of the chain produced a key. It wraps the
// go-secrets error, so errors.As reaches *secrets.Error and its Code and
// Guidance. Error() names the provider and the remedy; it never includes a
// credential or a backend's raw message.
type Error struct {
	ProviderID string
	Err        *secrets.Error
	// Hint is a remedy specific to this provider.
	Hint string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("no usable API key for provider %s: %s", e.ProviderID, e.Err.Error())
	if e.Hint != "" {
		msg += "; " + e.Hint
	}
	return msg
}
func (e *Error) Unwrap() error { return e.Err }

// Code is the stable machine-readable code ("secret_not_found", ...).
func (e *Error) Code() string { return e.Err.Code() }

// Guidance is the remedy to show an operator.
func (e *Error) Guidance() string {
	if e.Hint != "" {
		return e.Hint
	}
	return e.Err.Guidance()
}

// KeyRef returns the key_ref configured on a provider row, or "".
func (m *Manager) KeyRef(ctx context.Context, providerID string) (string, error) {
	if m.settings == nil {
		return "", nil
	}
	row, err := m.settings.GetProvider(ctx, providerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil // a fresh install has no provider rows until the first boot seeds them
	}
	if err != nil {
		return "", err
	}
	return readKeyRef(row.Settings), nil
}

func readKeyRef(settings string) string {
	if strings.TrimSpace(settings) == "" {
		return ""
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(settings), &doc) != nil {
		return ""
	}
	var ref string
	if raw, ok := doc[SettingsKey]; ok {
		_ = json.Unmarshal(raw, &ref)
	}
	return strings.TrimSpace(ref)
}

// ErrInvalidKeyRef is returned for a key_ref that is not a secret reference.
var ErrInvalidKeyRef = errors.New("key_ref is not a secret reference (keychain://, helper://, file://, op://); a key itself is saved with `provider set-key`, never in settings")

// ValidateSettings checks the key_ref in a provider row's settings JSON, if it
// has one. It does not echo the value: a literal key pasted into key_ref must
// not come back in an error or a log line.
func ValidateSettings(settings string) error {
	ref := readKeyRef(settings)
	if ref == "" {
		return nil
	}
	if _, err := secrets.ParseRef(ref); err != nil {
		return ErrInvalidKeyRef
	}
	return nil
}

// SetKeyRef stores (or, for "", clears) the provider's key_ref after checking
// that it parses. A reference names a secret and holds none, so a literal key
// is refused here instead of being saved into the settings.
func (m *Manager) SetKeyRef(ctx context.Context, providerID, ref string) error {
	if m.settings == nil {
		return errors.New("providerkeys: no settings store")
	}
	ref = strings.TrimSpace(ref)
	if ref != "" {
		if _, err := secrets.ParseRef(ref); err != nil {
			return ErrInvalidKeyRef
		}
	}
	row, err := m.settings.GetProvider(ctx, providerID)
	if err != nil {
		return err
	}
	doc := map[string]json.RawMessage{}
	if strings.TrimSpace(row.Settings) != "" {
		if jerr := json.Unmarshal([]byte(row.Settings), &doc); jerr != nil {
			return fmt.Errorf("provider %s settings are not a JSON object; not changing them", providerID)
		}
	}
	if ref == "" {
		delete(doc, SettingsKey)
	} else {
		b, _ := json.Marshal(ref)
		doc[SettingsKey] = b
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	s := string(out)
	return m.settings.UpdateProvider(ctx, providerID, store.ProviderUpdate{Settings: &s})
}

// candidates is the ordered chain of references for a provider.
func (m *Manager) candidates(ctx context.Context, providerID string) (refs []string, err error) {
	stored, err := m.KeyRef(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if stored != "" {
		refs = append(refs, stored)
	}
	if def := DefaultRef(providerID); def != stored {
		refs = append(refs, def)
	}
	if m.envOn && m.envNames != nil {
		if name := m.envNames(providerID); name != "" {
			refs = append(refs, "env://"+name)
		}
	}
	return refs, nil
}

// Resolve walks the chain and returns the first key found. A step that is
// merely empty (not found) falls through to the next; a step that is denied or
// unavailable is not hidden by a later empty one, and its error is returned.
func (m *Manager) Resolve(ctx context.Context, providerID string) (secrets.Secret, Report, error) {
	refs, err := m.candidates(ctx, providerID)
	if err != nil {
		return secrets.Secret{}, Report{}, err
	}
	var first *secrets.Error
	for _, ref := range refs {
		got, rerr := m.res.Resolve(ctx, ref)
		if rerr == nil {
			return got.Secret, Report{Ref: got.Ref, Source: got.Source}, nil
		}
		var se *secrets.Error
		if !errors.As(rerr, &se) {
			se = &secrets.Error{Kind: secrets.KindUnavailable, Ref: ref}
		}
		if first == nil || (first.Kind == secrets.KindNotFound && se.Kind != secrets.KindNotFound) {
			first = se
		}
		if se.Kind != secrets.KindNotFound && se.Kind != secrets.KindUnsupportedScheme {
			break
		}
	}
	if first == nil {
		first = &secrets.Error{Kind: secrets.KindNotFound, Ref: DefaultRef(providerID)}
	}
	return secrets.Secret{}, Report{}, &Error{ProviderID: providerID, Err: first, Hint: m.hint(providerID, first)}
}

// Func returns the deferred resolver an adapter calls on first use.
func (m *Manager) Func(providerID string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		sec, rep, err := m.Resolve(ctx, providerID)
		if err != nil {
			return "", err
		}
		slog.InfoContext(ctx, "provider key resolved", "provider", providerID, "source", rep.Source)
		return sec.Reveal(), nil
	}
}

func (m *Manager) hint(providerID string, se *secrets.Error) string {
	if se.Kind != secrets.KindNotFound {
		return ""
	}
	h := fmt.Sprintf("save one with `%s provider set-key %s` (key on stdin)", brand.BinaryName, providerID)
	if name := m.envHint(providerID); name != "" {
		h += fmt.Sprintf("; %s is set but the environment fallback is off (%s=1 enables it)", name, EnvEnableVar)
	}
	return h
}

// envHint names the conventional variable when it is set and ignored.
func (m *Manager) envHint(providerID string) string {
	if m.envOn || m.envNames == nil {
		return ""
	}
	name := m.envNames(providerID)
	if name == "" {
		return ""
	}
	if v, ok := m.lookup(name); ok && strings.TrimSpace(v) != "" {
		return name
	}
	return ""
}

// Status is what an operator sees for one provider. It carries no key.
type Status struct {
	ProviderID string `json:"provider_id"`
	Usable     bool   `json:"usable"`
	// Source is where the key came from: "keychain", "keychain:legacy:nanite",
	// "helper:<name>", "file", "op", "env:<VAR>". Empty when not usable.
	Source string `json:"source,omitempty"`
	// Ref is the reference that answered, or the first one tried.
	Ref string `json:"ref,omitempty"`
	// KeyRef is the key_ref configured on the provider row, if any.
	KeyRef string `json:"key_ref,omitempty"`
	// Code and Guidance explain a provider that is not usable.
	Code     string `json:"code,omitempty"`
	Guidance string `json:"guidance,omitempty"`
}

// Status resolves the provider's key, discards it and reports the outcome.
func (m *Manager) Status(ctx context.Context, providerID string) Status {
	st := Status{ProviderID: providerID}
	st.KeyRef, _ = m.KeyRef(ctx, providerID)
	sec, rep, err := m.Resolve(ctx, providerID)
	if err != nil {
		st.Ref = st.KeyRef
		if st.Ref == "" {
			st.Ref = DefaultRef(providerID)
		}
		var ke *Error
		if errors.As(err, &ke) {
			st.Code, st.Guidance = ke.Code(), ke.Guidance()
			if ke.Err.Ref != "" {
				st.Ref = ke.Err.Ref
			}
		} else {
			st.Code, st.Guidance = "secret_unavailable", err.Error()
		}
		return st
	}
	sec.Zero()
	st.Usable, st.Source, st.Ref = true, rep.Source, rep.Ref
	return st
}

// writeTarget is where set-key writes: the configured key_ref, else the
// default. A key_ref with a read-only scheme makes set-key fail with that
// scheme's typed error rather than write somewhere the operator did not name.
func (m *Manager) writeTarget(ctx context.Context, providerID string) (string, error) {
	stored, err := m.KeyRef(ctx, providerID)
	if err != nil {
		return "", err
	}
	if stored != "" {
		return stored, nil
	}
	return DefaultRef(providerID), nil
}

// ErrReadOnlyRef is returned when set-key or clear-key would write to a
// reference whose scheme Nanite cannot write.
var ErrReadOnlyRef = errors.New("this provider's key_ref names a source Nanite cannot write; change the key where it lives, or clear the key_ref to use the keychain")

// writable rejects a target whose scheme only reads, naming the scheme and not
// the rest of the reference.
func writable(target string) error {
	ref, err := secrets.ParseRef(target)
	if err != nil {
		return ErrInvalidKeyRef
	}
	if ref.Scheme == "keychain" || ref.Scheme == "keyring" {
		return nil
	}
	return fmt.Errorf("%w (scheme %s://)", ErrReadOnlyRef, ref.Scheme)
}

// SetKey saves the key read from r. The key never passes through an argument
// list or an error message.
func (m *Manager) SetKey(ctx context.Context, providerID string, r io.Reader) error {
	target, err := m.writeTarget(ctx, providerID)
	if err != nil {
		return err
	}
	if err := writable(target); err != nil {
		return err
	}
	return m.res.Set(ctx, target, r)
}

// ClearKey removes the saved key. It also removes the pre-reference keychain
// entry, which belongs to Nanite, so a cleared provider does not keep running
// on the old location.
func (m *Manager) ClearKey(ctx context.Context, providerID string) error {
	target, err := m.writeTarget(ctx, providerID)
	if err != nil {
		return err
	}
	if err := writable(target); err != nil {
		return err
	}
	if err := m.res.Delete(ctx, target); err != nil && secrets.KindOf(err) != secrets.KindNotFound {
		return err
	}
	if err := m.delLegacy(legacykeys.ProviderKeyName(providerID)); err != nil && secrets.KindOf(err) != secrets.KindNotFound {
		return err
	}
	return nil
}

// Resolver exposes the underlying resolver, for callers that resolve other
// references with the same policy.
func (m *Manager) Resolver() *secrets.Resolver { return m.res }
