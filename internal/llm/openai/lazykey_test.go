package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	secrets "github.com/hollis-labs/libs/security/secrets"
)

func lazyKeyServer(t *testing.T, envKey string) (requests *atomic.Int32, lastAuth *atomic.Value) {
	t.Helper()
	requests, lastAuth = new(atomic.Int32), new(atomic.Value)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		lastAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	t.Setenv("OPENAI_API_KEY", envKey)
	return requests, lastAuth
}

func TestSetKeyFunc_FetchesOnFirstUseOnceAndReuses(t *testing.T) {
	_, lastAuth := lazyKeyServer(t, "")
	c := New("", nil)
	var calls atomic.Int32
	c.SetKeyFunc(func(context.Context) (string, error) { calls.Add(1); return "lazy-key", nil })

	if calls.Load() != 0 {
		t.Fatal("the key was fetched at registration, not at first use")
	}
	for range 3 {
		if err := c.VerifyKey(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || lastAuth.Load() != "Bearer lazy-key" {
		t.Fatalf("fetched %d times, header %q", calls.Load(), lastAuth.Load())
	}
}

// New takes OPENAI_API_KEY when it is given no key. A client whose key is
// resolved by reference must not use it, whatever the failure.
func TestSetKeyFunc_NeverFallsBackToTheEnvironmentKey(t *testing.T) {
	requests, _ := lazyKeyServer(t, "sk-from-the-environment")
	c := New("", nil)
	c.SetKeyFunc(func(context.Context) (string, error) {
		return "", &secrets.Error{Kind: secrets.KindNotFound, Ref: "keychain://nanite/provider/openai-001"}
	})

	err := c.VerifyKey(context.Background())
	var se *secrets.Error
	if !errors.As(err, &se) || se.Kind != secrets.KindNotFound {
		t.Fatalf("err = %v, want the typed not-found error", err)
	}
	if requests.Load() != 0 {
		t.Fatal("a request went out using the environment key")
	}
}

func TestSetKeyFunc_EmptyResolvedKeyIsRefused(t *testing.T) {
	requests, _ := lazyKeyServer(t, "sk-from-the-environment")
	c := New("", nil)
	c.SetKeyFunc(func(context.Context) (string, error) { return "", nil })
	if err := c.VerifyKey(context.Background()); err == nil || requests.Load() != 0 {
		t.Fatalf("err = %v, requests = %d", err, requests.Load())
	}
}
