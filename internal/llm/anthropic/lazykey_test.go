package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	secrets "github.com/hollis-labs/libs/security/secrets"
)

func lazyKeyServer(t *testing.T) (requests *atomic.Int32, lastKey *atomic.Value) {
	t.Helper()
	requests, lastKey = new(atomic.Int32), new(atomic.Value)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		lastKey.Store(r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"has_more":false,"first_id":null,"last_id":null}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "")
	return requests, lastKey
}

func TestSetKeyFunc_FetchesOnFirstUseOnceAndReuses(t *testing.T) {
	_, lastKey := lazyKeyServer(t)
	c := New()
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
	if calls.Load() != 1 {
		t.Fatalf("key fetched %d times, want once", calls.Load())
	}
	if got := lastKey.Load(); got != "lazy-key" {
		t.Fatalf("request carried key %q", got)
	}
}

func TestSetKeyFunc_FailureIsTheTypedErrorSendsNothingAndIsRetried(t *testing.T) {
	requests, lastKey := lazyKeyServer(t)
	c := New()
	var calls atomic.Int32
	c.SetKeyFunc(func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return "", &secrets.Error{Kind: secrets.KindDenied, Ref: "keychain://nanite/provider/x"}
		}
		return "later-key", nil
	})

	err := c.VerifyKey(context.Background())
	var se *secrets.Error
	if !errors.As(err, &se) || se.Kind != secrets.KindDenied {
		t.Fatalf("err = %v, want the typed denied error", err)
	}
	if requests.Load() != 0 {
		t.Fatal("a request went out without a key")
	}
	if err := c.VerifyKey(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if lastKey.Load() != "later-key" {
		t.Fatalf("retry used key %q", lastKey.Load())
	}
}

func TestSetAPIKey_ReplacesALazyKey(t *testing.T) {
	_, lastKey := lazyKeyServer(t)
	c := New()
	c.SetKeyFunc(func(context.Context) (string, error) { return "lazy", nil })
	c.SetAPIKey("fixed")
	if err := c.VerifyKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lastKey.Load() != "fixed" {
		t.Fatalf("used %q", lastKey.Load())
	}
}
