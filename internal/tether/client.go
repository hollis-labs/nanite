// Package tether is the small registry and mailbox HTTP client used by Nanite.
package tether

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config is host-owned transport configuration; CredentialFile is never authored.
type Config struct {
	Endpoint       string
	CredentialFile string
	HTTPClient     *http.Client
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
	ID        string          `json:"id"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	Kind      string          `json:"kind"`
	ThreadID  string          `json:"thread_id"`
	Subject   string          `json:"subject"`
	Body      string          `json:"body"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

var ErrNotFound = errors.New("tether: not found")

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("tether: invalid endpoint")
	}
	if u.Scheme != "https" && u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("tether: HTTPS required except on loopback")
	}
	credential, err := readCredential(cfg.CredentialFile)
	if err != nil {
		return nil, err
	}
	hc := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{strings.TrimRight(u.String(), "/"), credential, &hc}, nil
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
	p := struct {
		Profile
		Substrate  string `json:"substrate"`
		ExternalID string `json:"external_id"`
	}{Profile: Profile{DisplayName: name}, Substrate: "nanite", ExternalID: externalID}
	err = c.request(ctx, "POST", "/registry/agents", p, &result.Agent)
	return result.Agent, err
}

// listPage is how many unread messages one List call returns.
const listPage = 50

// List fetches the oldest page of unread mail, oldest first. Tether lists
// newest first; its total lets us reach the tail without loading the backlog.
// Mail arriving or being read between two fetches shifts the tail, so the
// fetch repeats until the total it reports puts the tail at the offset used.
func (c *Client) List(ctx context.Context, urn string) ([]Message, error) {
	var result struct {
		Messages []Message `json:"messages"`
		Total    int       `json:"total"`
	}
	q := url.Values{"to": {urn}, "as": {urn}, "unread_only": {"true"}, "limit": {strconv.Itoa(listPage)}}
	for offset, attempt := 0, 0; ; attempt++ {
		if offset > 0 {
			q.Set("offset", strconv.Itoa(offset))
		} else {
			q.Del("offset")
		}
		result.Messages, result.Total = nil, 0
		if err := c.request(ctx, "GET", "/messages/list?"+q.Encode(), nil, &result); err != nil {
			return nil, err
		}
		want := max(result.Total-listPage, 0)
		if want == offset {
			break
		}
		if attempt == listAttempts {
			return nil, errors.New("tether: unread total kept changing")
		}
		offset = want
	}
	out := make([]Message, 0, len(result.Messages))
	// Walk the newest-first page backwards, so a stable sort keeps the wire
	// order for messages with equal timestamps.
	for i := len(result.Messages) - 1; i >= 0; i-- {
		if m := result.Messages[i]; m.ID != "" && m.To == urn {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// listAttempts bounds how often List refetches the tail while mail arrives.
const listAttempts = 3

// Endpoint returns the canonical endpoint used for durable registration keys.
func (c *Client) Endpoint() string { return c.endpoint }

// ProtectSecret registers the loaded credential without exposing it in config.
func (c *Client) ProtectSecret(add func(string, string)) { add("tether", c.credential) }

func readCredential(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- credential file explicitly selected by the host.
	if err != nil {
		return "", errors.New("tether: credential file unavailable")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("tether: credential must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("tether: credential file must be private")
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return "", errors.New("tether: credential file unreadable or too large")
	}
	value := strings.TrimSpace(string(b))
	if value == "" {
		return "", errors.New("tether: credential file empty")
	}
	return value, nil
}
func (c *Client) MarkRead(ctx context.Context, id, urn string) error {
	return c.request(ctx, "POST", "/messages/"+url.PathEscape(id)+"/read?as="+url.QueryEscape(urn), nil, nil)
}
