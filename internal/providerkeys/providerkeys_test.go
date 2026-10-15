package providerkeys_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	secrets "github.com/hollis-labs/go-secrets"
	"github.com/hollis-labs/go-secrets/secretstest"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/store"
)

const (
	pid   = "anthropic-001"
	key   = "sk-ant-api03-DO-NOT-LEAK-0123456789"
	other = "sk-ant-api03-OTHER-9876543210"
)

type fakeSettings struct {
	rows map[string]*store.ProviderConfig
}

func newSettings(settings string) *fakeSettings {
	return &fakeSettings{rows: map[string]*store.ProviderConfig{pid: {ID: pid, Settings: settings}}}
}
func (f *fakeSettings) GetProvider(_ context.Context, id string) (*store.ProviderConfig, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, errors.New("not found")
	}
	c := *r
	return &c, nil
}
func (f *fakeSettings) UpdateProvider(_ context.Context, id string, u store.ProviderUpdate) error {
	if u.Settings != nil {
		f.rows[id].Settings = *u.Settings
	}
	return nil
}

type env map[string]string

func (e env) lookup(k string) (string, bool) { v, ok := e[k]; return v, ok }

func harness(t *testing.T, settings string, e env, envOn bool) (*providerkeys.Manager, *secretstest.FakeStore, *fakeSettings, *[]string) {
	t.Helper()
	secretstest.Install(t)
	fs := &secretstest.FakeStore{}
	st := newSettings(settings)
	var deleted []string
	m := providerkeys.New(providerkeys.Options{
		Store:      fs,
		Settings:   st,
		EnvEnabled: envOn,
		EnvNames:   func(string) string { return "ANTHROPIC_API_KEY" },
		LookupEnv:  e.lookup,
		DeleteLegacy: func(a string) error {
			deleted = append(deleted, a)
			fs.Delete(context.Background(), "nanite", a) //nolint:errcheck
			return nil
		},
	})
	return m, fs, st, &deleted
}

func TestDefaultKeychainEntryResolvesAndReportsItsSource(t *testing.T) {
	m, fs, _, _ := harness(t, "", nil, false)
	fs.Put(secrets.DefaultService, "nanite/provider/"+pid, key)

	sec, rep, err := m.Resolve(context.Background(), pid)
	if err != nil || sec.Reveal() != key {
		t.Fatalf("got %v, %v", sec, err)
	}
	if rep.Source != "keychain" || rep.Ref != providerkeys.DefaultRef(pid) {
		t.Fatalf("report %+v", rep)
	}
}

func TestExistingInstallKeepsWorkingThroughTheLegacyAccount(t *testing.T) {
	m, fs, _, _ := harness(t, "", nil, false)
	fs.Put("nanite", "provider-api-key:"+pid, key)

	_, rep, err := m.Resolve(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Source != "keychain:legacy:nanite" {
		t.Fatalf("source %q", rep.Source)
	}
	for _, c := range fs.Calls() {
		if strings.HasPrefix(c, "set") || strings.HasPrefix(c, "delete") {
			t.Fatalf("resolution wrote to the store: %v", fs.Calls())
		}
	}
}

func TestStoredRefIsTriedBeforeTheDefault(t *testing.T) {
	m, fs, _, _ := harness(t, `{"key_ref":"keychain://work/anthropic"}`, nil, false)
	fs.Put(secrets.DefaultService, "work/anthropic", key)
	fs.Put(secrets.DefaultService, "nanite/provider/"+pid, other)

	sec, rep, err := m.Resolve(context.Background(), pid)
	if err != nil || sec.Reveal() != key || rep.Ref != "keychain://work/anthropic" {
		t.Fatalf("got %v %+v %v", sec, rep, err)
	}
}

func TestMissingStoredRefFallsThroughToTheDefault(t *testing.T) {
	m, fs, _, _ := harness(t, `{"key_ref":"keychain://work/anthropic"}`, nil, false)
	fs.Put(secrets.DefaultService, "nanite/provider/"+pid, key)

	_, rep, err := m.Resolve(context.Background(), pid)
	if err != nil || rep.Ref != providerkeys.DefaultRef(pid) {
		t.Fatalf("got %+v %v", rep, err)
	}
}

func TestEnvironmentIsIgnoredUnlessEnabled(t *testing.T) {
	m, _, _, _ := harness(t, "", env{"ANTHROPIC_API_KEY": key}, false)

	_, _, err := m.Resolve(context.Background(), pid)
	var pe *providerkeys.Error
	if !errors.As(err, &pe) || pe.Code() != "secret_not_found" {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(pe.Guidance(), "ANTHROPIC_API_KEY") || !strings.Contains(pe.Guidance(), providerkeys.EnvEnableVar) {
		t.Fatalf("guidance should say the variable is set but ignored: %q", pe.Guidance())
	}
	secretstest.AssertNoLeak(t, key, err, pe.Guidance())
}

func TestEnvironmentAnswersLastWhenEnabledAndSaysSo(t *testing.T) {
	m, _, _, _ := harness(t, "", env{"ANTHROPIC_API_KEY": key}, true)

	sec, rep, err := m.Resolve(context.Background(), pid)
	if err != nil || sec.Reveal() != key || rep.Source != "env:ANTHROPIC_API_KEY" {
		t.Fatalf("got %v %+v %v", sec, rep, err)
	}
}

func TestKeychainBeatsEnvironmentWhenBothExist(t *testing.T) {
	m, fs, _, _ := harness(t, "", env{"ANTHROPIC_API_KEY": other}, true)
	fs.Put(secrets.DefaultService, "nanite/provider/"+pid, key)

	sec, rep, _ := m.Resolve(context.Background(), pid)
	if sec.Reveal() != key || rep.Source != "keychain" {
		t.Fatalf("got %v %+v", sec, rep)
	}
}

func TestDeniedIsNotHiddenByALaterEmptyStep(t *testing.T) {
	m, fs, _, _ := harness(t, "", env{"ANTHROPIC_API_KEY": key}, true)
	fs.Fail = &secrets.Error{Kind: secrets.KindDenied}

	_, _, err := m.Resolve(context.Background(), pid)
	var se *secrets.Error
	if !errors.As(err, &se) || se.Kind != secrets.KindDenied {
		t.Fatalf("got %v, want denied (the environment must not mask a denied keychain)", err)
	}
}

func TestUnavailableKeychainIsATypedError(t *testing.T) {
	m, fs, _, _ := harness(t, "", nil, false)
	fs.Fail = &secrets.Error{Kind: secrets.KindUnavailable}

	st := m.Status(context.Background(), pid)
	if st.Usable || st.Code != "secret_unavailable" || st.Guidance == "" {
		t.Fatalf("status %+v", st)
	}
}

func TestStatusReportsUsableWithSourceAndNoKey(t *testing.T) {
	m, fs, _, _ := harness(t, "", nil, false)
	fs.Put(secrets.DefaultService, "nanite/provider/"+pid, key)

	st := m.Status(context.Background(), pid)
	if !st.Usable || st.Source != "keychain" {
		t.Fatalf("status %+v", st)
	}
	secretstest.AssertNoLeak(t, key, st)
}

func TestSetKeyWritesTheDefaultLocationFromTheReader(t *testing.T) {
	m, fs, _, _ := harness(t, "", nil, false)

	if err := m.SetKey(context.Background(), pid, strings.NewReader(key+"\n")); err != nil {
		t.Fatal(err)
	}
	if !fs.Has(secrets.DefaultService, "nanite/provider/"+pid) {
		t.Fatalf("not saved: %v", fs.Calls())
	}
	sec, _, err := m.Resolve(context.Background(), pid)
	if err != nil || sec.Reveal() != key {
		t.Fatalf("round trip: %v %v", sec, err)
	}
}

func TestSetKeyRefusesAnEmptyKeyAndNeverEchoesIt(t *testing.T) {
	m, _, _, _ := harness(t, "", nil, false)
	err := m.SetKey(context.Background(), pid, strings.NewReader("  \n"))
	if err == nil {
		t.Fatal("an empty key must be refused")
	}
}

func TestClearKeyRemovesTheDefaultAndTheLegacyEntry(t *testing.T) {
	m, fs, _, deleted := harness(t, "", nil, false)
	fs.Put(secrets.DefaultService, "nanite/provider/"+pid, key)
	fs.Put("nanite", "provider-api-key:"+pid, other)

	if err := m.ClearKey(context.Background(), pid); err != nil {
		t.Fatal(err)
	}
	if len(*deleted) != 1 || (*deleted)[0] != "provider-api-key:"+pid {
		t.Fatalf("legacy delete %v", *deleted)
	}
	if _, _, err := m.Resolve(context.Background(), pid); secrets.KindOf(errors.Unwrap(err)) != secrets.KindNotFound {
		t.Fatalf("a cleared provider must have no key, got %v", err)
	}
}

func TestSetKeyRefValidatesAndKeepsOtherSettings(t *testing.T) {
	m, _, st, _ := harness(t, `{"cli_path":"/usr/bin/x"}`, nil, false)

	if err := m.SetKeyRef(context.Background(), pid, "keychain://work/anthropic"); err != nil {
		t.Fatal(err)
	}
	got := st.rows[pid].Settings
	if !strings.Contains(got, `"cli_path":"/usr/bin/x"`) || !strings.Contains(got, `"key_ref":"keychain://work/anthropic"`) {
		t.Fatalf("settings %s", got)
	}
	if err := m.SetKeyRef(context.Background(), pid, ""); err != nil || strings.Contains(st.rows[pid].Settings, "key_ref") {
		t.Fatalf("clear: %v %s", err, st.rows[pid].Settings)
	}
}

func TestSetKeyRefRefusesALiteralKeyWithoutEchoingIt(t *testing.T) {
	m, _, st, _ := harness(t, "", nil, false)
	err := m.SetKeyRef(context.Background(), pid, key)
	if err == nil {
		t.Fatal("a literal key is not a reference")
	}
	secretstest.AssertNoLeak(t, key, err)
	if strings.Contains(st.rows[pid].Settings, key) {
		t.Fatal("the literal was saved")
	}
}

func TestResolvedKeyIsRegisteredWithTheRedactor(t *testing.T) {
	m, fs, _, _ := harness(t, "", nil, false)
	fs.Put(secrets.DefaultService, "nanite/provider/"+pid, key)
	if _, _, err := m.Resolve(context.Background(), pid); err != nil {
		t.Fatal(err)
	}
	if got := providerkeys.Redactor().Redact("auth failed for " + key); strings.Contains(got, key) {
		t.Fatalf("not redacted: %s", got)
	}
}

func TestAFreshInstallWithNoProviderRowsHasNoKeyRefAndIsNotAnError(t *testing.T) {
	secretstest.Install(t)
	m := providerkeys.New(providerkeys.Options{Store: &secretstest.FakeStore{}, Settings: missingRows{}})

	st := m.Status(context.Background(), pid)
	if st.Code != "secret_not_found" || st.Ref != providerkeys.DefaultRef(pid) {
		t.Fatalf("status %+v: a missing provider row must read as 'no key_ref', not as a failure", st)
	}
}

type missingRows struct{}

func (missingRows) GetProvider(context.Context, string) (*store.ProviderConfig, error) {
	return nil, sql.ErrNoRows
}
func (missingRows) UpdateProvider(context.Context, string, store.ProviderUpdate) error {
	return sql.ErrNoRows
}

func TestSetAndClearKeyRefuseAReadOnlyKeyRefWithoutWritingElsewhere(t *testing.T) {
	m, fs, _, _ := harness(t, `{"key_ref":"helper://vault/anthropic/work"}`, nil, false)

	for name, err := range map[string]error{
		"set":   m.SetKey(context.Background(), pid, strings.NewReader(key)),
		"clear": m.ClearKey(context.Background(), pid),
	} {
		if !errors.Is(err, providerkeys.ErrReadOnlyRef) || !strings.Contains(err.Error(), "helper://") {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(err.Error(), "vault") {
			t.Fatalf("%s: the error names more of the reference than its scheme: %v", name, err)
		}
	}
	if len(fs.Calls()) != 0 {
		t.Fatalf("the keychain was written instead: %v", fs.Calls())
	}
}
