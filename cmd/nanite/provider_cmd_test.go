package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	secrets "github.com/hollis-labs/go-secrets"
	"github.com/hollis-labs/go-secrets/secretstest"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

const cliKey = "sk-ant-api03-CLI-MUST-NOT-APPEAR-0123456789"

type cliSettings map[string]*store.ProviderConfig

func (c cliSettings) GetProvider(_ context.Context, id string) (*store.ProviderConfig, error) {
	if r, ok := c[id]; ok {
		cp := *r
		return &cp, nil
	}
	return nil, errors.New("not found")
}
func (c cliSettings) UpdateProvider(_ context.Context, id string, u store.ProviderUpdate) error {
	if u.Settings != nil {
		c[id].Settings = *u.Settings
	}
	return nil
}

type cliHarness struct {
	fs   *secretstest.FakeStore
	keys *providerkeys.Manager
	set  cliSettings
}

func newCLIHarness(t *testing.T, env map[string]string, envOn bool) *cliHarness {
	t.Helper()
	secretstest.Install(t)
	h := &cliHarness{fs: &secretstest.FakeStore{}, set: cliSettings{
		"anthropic-001": {ID: "anthropic-001"}, "openai-001": {ID: "openai-001"},
	}}
	h.keys = providerkeys.New(providerkeys.Options{
		Store: h.fs, Settings: h.set, EnvEnabled: envOn, EnvNames: service.ProviderKeyEnvName,
		LookupEnv:    func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		DeleteLegacy: func(a string) error { return h.fs.Delete(context.Background(), "nanite", a) },
	})
	return h
}

func (h *cliHarness) run(stdin string, tty bool, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = runProvider(context.Background(), args, h.keys, strings.NewReader(stdin), tty, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestProviderSetKeyReadsStdinSavesAndReportsTheSource(t *testing.T) {
	h := newCLIHarness(t, nil, false)

	code, out, errOut := h.run(cliKey+"\n", false, "set-key", "anthropic")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "resolves from keychain") {
		t.Fatalf("output %q", out)
	}
	secretstest.AssertNoLeak(t, cliKey, out, errOut)
	if !h.fs.Has(secrets.DefaultService, "nanite/provider/anthropic-001") {
		t.Fatal("key not saved at the default reference")
	}
}

func TestProviderSetKeyRefusesATerminalAndAnArgument(t *testing.T) {
	h := newCLIHarness(t, nil, false)

	code, _, errOut := h.run("", true, "set-key", "anthropic")
	if code == 0 || !strings.Contains(errOut, "stdin") {
		t.Fatalf("a terminal must be refused with guidance: %d %q", code, errOut)
	}
	// The key as a second argument is not accepted, and not echoed.
	code, out, errOut := h.run("", false, "set-key", "anthropic", cliKey)
	if code == 0 {
		t.Fatal("a key argument must be refused")
	}
	secretstest.AssertNoLeak(t, cliKey, out, errOut)
	if len(h.fs.Calls()) != 0 {
		t.Fatalf("the store was touched: %v", h.fs.Calls())
	}
}

func TestProviderListShowsUsabilityAndSourceNeverTheKey(t *testing.T) {
	h := newCLIHarness(t, nil, false)
	h.fs.Put("nanite", "provider-api-key:anthropic-001", cliKey) // an existing install

	code, out, _ := h.run("", false, "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "anthropic-001") || !strings.Contains(out, "keychain:legacy:nanite") || !strings.Contains(out, "openai-001") {
		t.Fatalf("output:\n%s", out)
	}
	secretstest.AssertNoLeak(t, cliKey, out)
}

func TestProviderStatusExplainsAndExitsNonzeroWhenNotUsable(t *testing.T) {
	h := newCLIHarness(t, map[string]string{"OPENAI_API_KEY": cliKey}, false)

	code, out, _ := h.run("", false, "status", "openai")
	if code != 1 {
		t.Fatalf("exit %d, want 1 for an unusable provider", code)
	}
	for _, want := range []string{"secret_not_found", "provider set-key openai-001", "OPENAI_API_KEY", providerkeys.EnvEnableVar} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	secretstest.AssertNoLeak(t, cliKey, out)
}

func TestProviderStatusJSONIsMachineReadableAndKeyFree(t *testing.T) {
	h := newCLIHarness(t, map[string]string{"ANTHROPIC_API_KEY": cliKey}, true)

	code, out, _ := h.run("", false, "status", "anthropic", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rows []providerkeys.Status
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("json: %v %q", err, out)
	}
	if !rows[0].Usable || rows[0].Source != "env:ANTHROPIC_API_KEY" {
		t.Fatalf("row %+v", rows[0])
	}
	secretstest.AssertNoLeak(t, cliKey, out)
}

func TestProviderSetRefAndClear(t *testing.T) {
	h := newCLIHarness(t, nil, false)
	h.fs.Put(secrets.DefaultService, "work/anthropic", cliKey)

	if code, out, errOut := h.run("", false, "set-ref", "anthropic", "keychain://work/anthropic"); code != 0 || !strings.Contains(out, "resolves from keychain") {
		t.Fatalf("set-ref: %d %q %q", code, out, errOut)
	}
	if !strings.Contains(h.set["anthropic-001"].Settings, "keychain://work/anthropic") {
		t.Fatalf("settings %s", h.set["anthropic-001"].Settings)
	}
	if code, _, _ := h.run("", false, "set-ref", "anthropic", "--clear"); code != 0 || strings.Contains(h.set["anthropic-001"].Settings, "key_ref") {
		t.Fatalf("clear: %d %s", code, h.set["anthropic-001"].Settings)
	}
	// A literal key is not a reference.
	code, out, errOut := h.run("", false, "set-ref", "anthropic", cliKey)
	if code == 0 {
		t.Fatal("a literal key must be refused as a reference")
	}
	secretstest.AssertNoLeak(t, cliKey, out, errOut, h.set["anthropic-001"].Settings)
}

func TestProviderClearKeyAndUnknownProvider(t *testing.T) {
	h := newCLIHarness(t, nil, false)
	h.fs.Put(secrets.DefaultService, "nanite/provider/anthropic-001", cliKey)

	if code, out, errOut := h.run("", false, "clear-key", "anthropic-001"); code != 0 || !strings.Contains(out, "removed") {
		t.Fatalf("clear-key: %d %q %q", code, out, errOut)
	}
	if h.fs.Has(secrets.DefaultService, "nanite/provider/anthropic-001") {
		t.Fatal("key still saved")
	}
	if code, _, errOut := h.run("", false, "status", "nope"); code == 0 || !strings.Contains(errOut, "unknown provider") {
		t.Fatalf("unknown provider: %d %q", code, errOut)
	}
}

func TestProviderSetKeyOnADeniedKeychainSaysSoWithoutTheKey(t *testing.T) {
	h := newCLIHarness(t, nil, false)
	h.fs.Fail = &secrets.Error{Kind: secrets.KindDenied}

	code, out, errOut := h.run(cliKey, false, "set-key", "anthropic")
	if code == 0 || !strings.Contains(errOut, "secret_denied") {
		t.Fatalf("%d %q", code, errOut)
	}
	secretstest.AssertNoLeak(t, cliKey, out, errOut)
}
