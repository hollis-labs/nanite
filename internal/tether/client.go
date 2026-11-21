// Package tether is the small registry and mailbox HTTP client used by Nanite.
package tether

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config is host-owned transport configuration; Credential is never authored.
type Config struct {
	Endpoint   string
	Credential string
	HTTPClient *http.Client
}

type Client struct {
	endpoint, credential string
	http                 *http.Client
}
type Profile struct {
	URN         string       `json:"urn"`
	DisplayName string       `json:"display_name"`
	ExternalIDs []ExternalID `json:"external_ids,omitempty"`
}
type ExternalID struct {
	Substrate  string `json:"substrate"`
	ExternalID string `json:"external_id"`
}
type Message struct {
	ID       string          `json:"id"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Kind     string          `json:"kind"`
	ThreadID string          `json:"thread_id"`
	Subject  string          `json:"subject"`
	Body     string          `json:"body"`
	Payload  json.RawMessage `json:"payload"`
}

var ErrNotFound = errors.New("tether: not found")

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("tether: invalid endpoint")
	}
	if strings.TrimSpace(cfg.Credential) == "" {
		return nil, errors.New("tether: credential required")
	}
	hc := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{strings.TrimRight(cfg.Endpoint, "/"), cfg.Credential, &hc}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(b))
	if err != nil {
		return errors.New("tether: invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("tether: request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tether: HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}
func (c *Client) Lookup(ctx context.Context, urn string) (Profile, error) {
	var p Profile
	err := c.request(ctx, "GET", "/registry/agents/"+url.PathEscape(urn), nil, &p)
	return p, err
}
func (c *Client) Resolve(ctx context.Context, externalID, name string) (Profile, error) {
	var result struct {
		Agent Profile `json:"agent"`
	}
	q := url.Values{"external_id": {externalID}, "substrate": {"nanite"}}
	err := c.request(ctx, "GET", "/registry/agents?"+q.Encode(), nil, &result)
	if err == nil {
		if result.Agent.URN == "" {
			return Profile{}, errors.New("tether: registry response lacks agent")
		}
		return result.Agent, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Profile{}, err
	}
	p := Profile{DisplayName: name, ExternalIDs: []ExternalID{{Substrate: "nanite", ExternalID: externalID}}}
	err = c.request(ctx, "POST", "/registry/agents", p, &result.Agent)
	return result.Agent, err
}
func (c *Client) List(ctx context.Context, urn string) ([]Message, error) {
	var result struct {
		Messages []Message `json:"messages"`
	}
	q := url.Values{"to": {urn}, "as": {urn}, "unread_only": {"true"}, "limit": {"50"}}
	err := c.request(ctx, "GET", "/messages/list?"+q.Encode(), nil, &result)
	if err != nil {
		return nil, err
	}
	for _, m := range result.Messages {
		if m.ID == "" || m.To != urn {
			return nil, errors.New("tether: invalid mailbox response")
		}
	}
	return result.Messages, nil
}
func (c *Client) MarkRead(ctx context.Context, id, urn string) error {
	return c.request(ctx, "POST", "/messages/"+url.PathEscape(id)+"/read?as="+url.QueryEscape(urn), nil, nil)
}
