package openai

import (
	"context"
	"errors"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// KeyFunc supplies the API key at the moment a request first needs it.
type KeyFunc func(context.Context) (string, error)

// SetKeyFunc defers key resolution to first use: nothing is fetched here, the
// first request calls fn and builds the SDK client with the answer, and later
// requests reuse it. A failure is returned to that request and retried by the
// next one. SetAPIKey replaces it.
//
// It also drops the key New took from OPENAI_API_KEY: a client whose key is
// resolved by reference must not quietly use the environment instead.
func (c *Client) SetKeyFunc(fn KeyFunc) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	c.keyFn, c.keyLoaded = fn, false
	c.apiKey = ""
	c.sdk = sdk.NewClient(option.WithAPIKey(""), option.WithMaxRetries(0))
}

// client returns the SDK client for one request, resolving a deferred key
// first. The error is the key function's own, unwrapped, so a typed secret
// error reaches the caller intact.
func (c *Client) client(ctx context.Context) (sdk.Client, error) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	if c.keyFn != nil && !c.keyLoaded {
		key, err := c.keyFn(ctx)
		if err != nil {
			return sdk.Client{}, err
		}
		c.apiKey, c.keyLoaded = key, true
		c.sdk = sdk.NewClient(option.WithAPIKey(key), option.WithMaxRetries(0))
	}
	if c.keyFn != nil && c.apiKey == "" {
		return sdk.Client{}, errors.New("OPENAI_API_KEY not set")
	}
	return c.sdk, nil
}
