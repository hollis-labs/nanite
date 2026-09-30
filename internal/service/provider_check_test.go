package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/llm/keycheck"
	"github.com/hollis-labs/nanite/internal/store"
)

type fakeVerifier struct {
	err      error
	calls    int
	deadline time.Duration
}

func (f *fakeVerifier) VerifyKey(ctx context.Context) error {
	f.calls++
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	return f.err
}

func newProviderCheckService(t *testing.T, key string, v *fakeVerifier) (*ProviderConfigService, *store.Store, *[]string) {
	t.Helper()
	st := newTestStore(t)
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.Seed(context.Background()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := st.SeedProviders(context.Background()); err != nil {
		t.Fatalf("SeedProviders: %v", err)
	}
	svc := NewProviderConfigService(st)
	svc.resolveKey = func(string, string) (string, string) {
		if key == "" {
			return "", ""
		}
		return key, APIKeySourceKeychain
	}
	var gotKeys []string
	svc.newVerifier = func(_ APIProviderSpec, k string) keyVerifier {
		gotKeys = append(gotKeys, k)
		return v
	}
	return svc, st, &gotKeys
}

func TestTestConnection_APIProviderOutcomes(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		verifyErr  error
		wantOK     bool
		wantStatus string
		wantCalls  int
	}{
		{"accepted", "sk-good", nil, true, ProviderCheckAccepted, 1},
		{"rejected", "sk-bad", fmt.Errorf("%w: 401", keycheck.ErrRejected), false, ProviderCheckRejected, 1},
		{"unreachable", "sk-any", errors.New("dial tcp: no route to host"), false, ProviderCheckUnreachable, 1},
		{"no key makes no call", "", nil, false, ProviderCheckNoKey, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &fakeVerifier{err: tc.verifyErr}
			svc, _, gotKeys := newProviderCheckService(t, tc.key, v)

			got, err := svc.TestConnection(context.Background(), "anthropic-001")
			if err != nil {
				t.Fatalf("TestConnection: %v", err)
			}
			if got.OK != tc.wantOK || got.Status != tc.wantStatus || got.Message == "" {
				t.Fatalf("result = %+v, want ok=%v status=%q with a message", got, tc.wantOK, tc.wantStatus)
			}
			if v.calls != tc.wantCalls {
				t.Fatalf("verifier calls = %d, want %d", v.calls, tc.wantCalls)
			}
			if tc.wantCalls > 0 {
				if len(*gotKeys) != 1 || (*gotKeys)[0] != tc.key {
					t.Fatalf("verifier built with keys %q, want [%q]", *gotKeys, tc.key)
				}
				if v.deadline <= 0 || v.deadline > providerCheckTimeout {
					t.Fatalf("check deadline = %v, want within %v", v.deadline, providerCheckTimeout)
				}
			}
		})
	}
}

func TestTestConnection_UnknownRowIsAnError(t *testing.T) {
	v := &fakeVerifier{}
	svc, _, _ := newProviderCheckService(t, "sk", v)
	if _, err := svc.TestConnection(context.Background(), "no-such-provider"); err == nil {
		t.Fatal("TestConnection on an unknown row returned no error")
	}
	if v.calls != 0 {
		t.Fatalf("verifier calls = %d, want 0", v.calls)
	}
}

func TestTestConnection_AdapterlessRowIsUnsupported(t *testing.T) {
	v := &fakeVerifier{}
	svc, st, _ := newProviderCheckService(t, "sk", v)
	// Seeding stopped writing adapter-less API rows (SP-20260508-0001), but
	// older databases still carry them and the settings UI lists them.
	if _, err := st.DB.Exec(`INSERT INTO providers (id, name, provider_type) VALUES ('gemini-api-001', 'Gemini', 'gemini')`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	got, err := svc.TestConnection(context.Background(), "gemini-api-001")
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if got.OK || got.Status != ProviderCheckUnsupported {
		t.Fatalf("result = %+v, want not ok, status %q", got, ProviderCheckUnsupported)
	}
	if v.calls != 0 {
		t.Fatalf("verifier calls = %d, want 0", v.calls)
	}
}

func TestTestConnection_CLIRowReportsDetectionOnly(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // the stand-in CLI binary must be executable, in t.TempDir()
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		path       string
		wantOK     bool
		wantStatus string
	}{
		{"found", bin, true, ProviderCheckCLIFound},
		{"not found", filepath.Join(t.TempDir(), "missing"), false, ProviderCheckCLINotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &fakeVerifier{}
			svc, st, _ := newProviderCheckService(t, "sk", v)
			settings := fmt.Sprintf(`{"cli_path":%q}`, tc.path)
			if err := st.UpdateProvider(context.Background(), "pty-001", store.ProviderUpdate{Settings: &settings}); err != nil {
				t.Fatalf("UpdateProvider: %v", err)
			}

			got, err := svc.TestConnection(context.Background(), "pty-001")
			if err != nil {
				t.Fatalf("TestConnection: %v", err)
			}
			if got.OK != tc.wantOK || got.Status != tc.wantStatus {
				t.Fatalf("result = %+v, want ok=%v status=%q", got, tc.wantOK, tc.wantStatus)
			}
			if tc.wantOK && got.Path != tc.path {
				t.Fatalf("path = %q, want %q", got.Path, tc.path)
			}
			if v.calls != 0 {
				t.Fatalf("a CLI row must not run a key check; verifier calls = %d", v.calls)
			}
		})
	}
}
