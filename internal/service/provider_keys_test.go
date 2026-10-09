package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/memory"
	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
	llmcontracts "github.com/hollis-labs/substrate/llm-core/llmcontracts"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// keyFixture is a ProviderConfigService whose keychain and environment are
// plain maps. The keychain map is deliberately unsynchronized: SetAPIKey's
// own lock is the only thing keeping concurrent saves off it, so -race sees
// it if that lock goes.
type keyFixture struct {
	svc      *ProviderConfigService
	keychain map[string]string
	env      map[string]string
	reg      *provider.Registry
	cat      *providercatalog.Catalog
}

func newKeyFixture(t *testing.T) *keyFixture {
	t.Helper()
	f := &keyFixture{
		keychain: map[string]string{},
		env:      map[string]string{},
		reg:      provider.NewRegistry(),
		cat:      providercatalog.New(),
	}
	f.svc = NewProviderConfigService(nil)
	f.svc.setSecret = func(k, v string) error { f.keychain[k] = v; return nil }
	f.svc.deleteSecret = func(k string) error { delete(f.keychain, k); return nil }
	f.svc.resolveKey = func(providerID, envKey string) (string, string) {
		return resolveAPIKey(func(k string) string { return f.keychain[k] }, func(k string) string { return f.env[k] }, providerID, envKey)
	}
	f.svc.SetProviderRuntime(f.reg, f.cat)
	return f
}

func (f *keyFixture) registered(t *testing.T) (llmcontracts.Provider, bool) {
	t.Helper()
	p, ok := f.reg.Get("anthropic")
	_, inCatalog := f.cat.Get("anthropic")
	if ok != inCatalog {
		t.Fatalf("registry has anthropic=%v but catalog has it=%v; they must agree", ok, inCatalog)
	}
	return p, ok
}

func TestSetAPIKey_SaveRegistersAFreshAdapterEachTime(t *testing.T) {
	f := newKeyFixture(t)
	ctx := context.Background()

	res, err := f.svc.SetAPIKey(ctx, "anthropic-001", "sk-one")
	if err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	if res != (APIKeyResult{HasKey: true, KeySource: APIKeySourceKeychain}) {
		t.Fatalf("result = %+v", res)
	}
	if f.keychain[secrets.ProviderKeyName("anthropic-001")] != "sk-one" {
		t.Fatalf("keychain = %v", f.keychain)
	}
	first, ok := f.registered(t)
	if !ok || first == nil {
		t.Fatal("anthropic not registered after saving a key")
	}

	if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", "sk-two"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	second, ok := f.registered(t)
	if !ok || second == first {
		t.Fatal("a new key must register a new adapter, not reuse the old one")
	}
}

func TestSetAPIKey_ClearWithEnvKeyKeepsProviderOnEnvironment(t *testing.T) {
	f := newKeyFixture(t)
	f.env["ANTHROPIC_API_KEY"] = "sk-env"
	ctx := context.Background()
	if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", "sk-keychain"); err != nil {
		t.Fatal(err)
	}
	before, _ := f.registered(t)

	res, err := f.svc.SetAPIKey(ctx, "anthropic-001", "")
	if err != nil {
		t.Fatalf("SetAPIKey clear: %v", err)
	}
	if res != (APIKeyResult{HasKey: false, KeySource: APIKeySourceEnvironment}) {
		t.Fatalf("result = %+v, want has_key=false on the environment key", res)
	}
	if _, ok := f.keychain[secrets.ProviderKeyName("anthropic-001")]; ok {
		t.Fatal("keychain entry not deleted")
	}
	after, ok := f.registered(t)
	if !ok || after == before {
		t.Fatal("clearing the keychain key with an env key set must re-register on the env key")
	}
}

// Clearing a key and saving it again must not reorder the model pickers:
// the entry goes back at its registration-order position.
func TestSetAPIKey_ResaveKeepsCatalogInSpecOrder(t *testing.T) {
	f := newKeyFixture(t)
	ctx := context.Background()
	for _, id := range []string{"anthropic-001", "openai-001"} {
		if _, err := f.svc.SetAPIKey(ctx, id, "k-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", "k-again"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range f.cat.List() {
		got = append(got, e.Name)
	}
	if strings.Join(got, ",") != "anthropic,openai" {
		t.Fatalf("catalog order after clear+save = %v, want anthropic,openai", got)
	}
	if _, ok := f.reg.Get("openai"); !ok {
		t.Fatal("re-ordering the catalog must not touch openai's registration")
	}
}

func TestSetAPIKey_ClearWithNoKeyLeftUnregisters(t *testing.T) {
	f := newKeyFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", "sk-one"); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.SetAPIKey(ctx, "anthropic-001", "")
	if err != nil {
		t.Fatalf("SetAPIKey clear: %v", err)
	}
	if res != (APIKeyResult{}) {
		t.Fatalf("result = %+v, want no key, no source", res)
	}
	if _, ok := f.registered(t); ok {
		t.Fatal("anthropic still registered with no key anywhere")
	}
}

func TestSetAPIKey_AdapterlessRowOnlyWritesKeychain(t *testing.T) {
	f := newKeyFixture(t)
	res, err := f.svc.SetAPIKey(context.Background(), "gemini-api-001", "g-key")
	if err != nil {
		t.Fatal(err)
	}
	if res != (APIKeyResult{HasKey: true, KeySource: APIKeySourceKeychain}) {
		t.Fatalf("result = %+v", res)
	}
	if f.keychain[secrets.ProviderKeyName("gemini-api-001")] != "g-key" {
		t.Fatal("keychain not written")
	}
	if names := f.reg.Names(); len(names) != 0 {
		t.Fatalf("registry touched for an adapter-less row: %v", names)
	}
}

func TestSetAPIKey_KeychainFailureChangesNothing(t *testing.T) {
	f := newKeyFixture(t)
	f.svc.setSecret = func(string, string) error { return errors.New("dbus-launch: not found") }
	if _, err := f.svc.SetAPIKey(context.Background(), "anthropic-001", "sk"); err == nil {
		t.Fatal("SetAPIKey succeeded although the keychain write failed")
	}
	if _, ok := f.registered(t); ok {
		t.Fatal("registered a provider although its key could not be stored")
	}
}

func TestSetAPIKey_NoRuntimeOnlyWritesKeychain(t *testing.T) {
	f := newKeyFixture(t)
	f.svc.SetProviderRuntime(nil, nil)
	res, err := f.svc.SetAPIKey(context.Background(), "anthropic-001", "sk")
	if err != nil || res.KeySource != APIKeySourceKeychain {
		t.Fatalf("SetAPIKey = %+v, %v", res, err)
	}
	if _, ok := f.reg.Get("anthropic"); ok {
		t.Fatal("registry written although no runtime is wired")
	}
}

// Concurrent saves and clears, with chat-side Gets running throughout. Run
// under -race: the fixture's keychain map is guarded only by SetAPIKey's
// lock. At the end the registry must agree with the keychain.
func TestSetAPIKey_ConcurrentSavesStayConsistent(t *testing.T) {
	f := newKeyFixture(t)
	ctx := context.Background()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if p, ok := f.reg.Get("anthropic"); ok && p == nil {
						t.Error("registry returned a nil adapter")
					}
					_ = f.cat.List()
				}
			}
		}()
	}

	var writers sync.WaitGroup
	for i := range 32 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			key := fmt.Sprintf("sk-%d", i)
			if i%3 == 0 {
				key = ""
			}
			if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", key); err != nil {
				t.Errorf("SetAPIKey: %v", err)
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	_, hasKey := f.keychain[secrets.ProviderKeyName("anthropic-001")]
	if _, ok := f.registered(t); ok != hasKey {
		t.Fatalf("registry has anthropic=%v, keychain has a key=%v", ok, hasKey)
	}
}

type recordingProvider struct{ name string }

func (p *recordingProvider) StreamChat(context.Context, llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return nil, errors.New("unused")
}

func (p *recordingProvider) Complete(_ context.Context, req llmtypes.ChatRequest) (string, error) {
	return p.name + ":" + req.Model, nil
}

func (p *recordingProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

// The memory extractor's call resolves its provider per use, so it follows a
// hot-swapped adapter, and is silently unavailable while none is registered.
func TestNewUtilityCall_ResolvesProviderPerUse(t *testing.T) {
	reg := provider.NewRegistry()
	call := newUtilityCall(reg, "anthropic", "haiku")
	ctx := context.Background()

	if _, err := call(ctx, "p"); !errors.Is(err, memory.ErrUtilityUnavailable) {
		t.Fatalf("unregistered provider: err = %v, want ErrUtilityUnavailable", err)
	}

	reg.Register("anthropic", &recordingProvider{name: "first"})
	if got, err := call(ctx, "p"); err != nil || !strings.HasPrefix(got, "first:haiku") {
		t.Fatalf("after register: %q, %v", got, err)
	}

	reg.Register("anthropic", &recordingProvider{name: "second"})
	if got, err := call(ctx, "p"); err != nil || !strings.HasPrefix(got, "second:haiku") {
		t.Fatalf("after swap: %q, %v — the call must not keep the adapter it first saw", got, err)
	}
}

func TestSetAPIKey_CredentialFailurePreservesRuntimeAndCause(t *testing.T) {
	for _, operation := range []string{"save", "clear"} {
		t.Run(operation, func(t *testing.T) {
			f := newKeyFixture(t)
			ctx := context.Background()
			if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", "retained-key"); err != nil {
				t.Fatal(err)
			}
			previous, _ := f.registered(t)
			cause := errors.New("backend error containing private-credential")
			f.svc.setSecret = func(string, string) error { return cause }
			f.svc.deleteSecret = func(string) error { return cause }
			key := "private-credential"
			if operation == "clear" {
				key = ""
			}
			_, err := f.svc.SetAPIKey(ctx, "anthropic-001", key)
			var unavailable *ProviderCredentialStoreError
			if !errors.As(err, &unavailable) || !errors.Is(err, cause) || unavailable.Operation != operation || unavailable.EnvironmentVariable != "ANTHROPIC_API_KEY" {
				t.Fatalf("missing typed error/cause: %v", err)
			}
			if strings.Contains(err.Error(), "private-credential") || !strings.Contains(err.Error(), "restart Nanite") {
				t.Fatalf("unsafe or unhelpful error: %v", err)
			}
			current, ok := f.registered(t)
			if !ok || current != previous || f.keychain[secrets.ProviderKeyName("anthropic-001")] != "retained-key" {
				t.Fatal("failed key update changed retained runtime/credential")
			}
		})
	}
}

func TestSetAPIKey_CanceledRequestDoesNotTouchCredentialOrRuntime(t *testing.T) {
	f := newKeyFixture(t)
	if _, err := f.svc.SetAPIKey(context.Background(), "anthropic-001", "retained-key"); err != nil {
		t.Fatal(err)
	}
	previous, _ := f.registered(t)
	f.svc.setSecret = func(string, string) error { t.Fatal("canceled save reached credential store"); return nil }
	f.svc.deleteSecret = func(string) error { t.Fatal("canceled clear reached credential store"); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, key := range []string{"replacement", ""} {
		if _, err := f.svc.SetAPIKey(ctx, "anthropic-001", key); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request: %v", err)
		}
	}
	current, ok := f.registered(t)
	if !ok || current != previous || f.keychain[secrets.ProviderKeyName("anthropic-001")] != "retained-key" {
		t.Fatal("canceled request changed runtime or credential")
	}
}
