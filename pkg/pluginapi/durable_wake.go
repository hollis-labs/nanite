package pluginapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

const CapabilityDurableWake = "durable_agent.wake"
const DurableWakeProtocol = 1
const MaxDurableWakeBytes = 64 << 10

// DurableWakeScope authorizes a real wake of explicit database-backed durable
// instance slugs. It permits no provisioning, profile edits or arbitrary tools.
type DurableWakeScope struct {
	AgentSlugs []string `json:"agent_slugs"`
}

func (scope DurableWakeScope) Validate() error {
	if len(scope.AgentSlugs) == 0 || len(scope.AgentSlugs) > 64 {
		return fmt.Errorf("pluginapi: wake scope requires one to 64 agent slugs")
	}
	seen := map[string]bool{}
	for _, slug := range scope.AgentSlugs {
		if !agentSlug.MatchString(slug) || seen[slug] {
			return fmt.Errorf("pluginapi: invalid or duplicate wake agent slug")
		}
		seen[slug] = true
	}
	return nil
}
func DecodeDurableWakeScope(raw json.RawMessage) (DurableWakeScope, error) {
	var scope DurableWakeScope
	if len(raw) == 0 || len(raw) > maxQueryScopeBytes {
		return scope, fmt.Errorf("pluginapi: wake scope absent or oversized")
	}
	if err := manifest.DecodeExtension(raw, &scope); err != nil {
		return scope, fmt.Errorf("pluginapi: invalid wake scope")
	}
	return scope, scope.Validate()
}
func (scope DurableWakeScope) Allows(slug string) bool {
	return agentSlug.MatchString(slug) && slices.Contains(scope.AgentSlugs, slug)
}

// DurableWakeGrant belongs to one accepted subprocess connection. The host
// revokes it on unload or permanent failure. Never log or persist the token.
type DurableWakeGrant struct {
	Protocol int              `json:"protocol"`
	PluginID string           `json:"plugin_id"`
	HostURL  string           `json:"host_url"`
	Token    string           `json:"token"`
	Scope    DurableWakeScope `json:"scope"`
}

var ErrDurableWakeNotGranted = errors.New("pluginapi: durable wakes were not granted")

type hostIdentity struct {
	HostQuery   json.RawMessage `json:"nanite_host_query,omitempty"`
	DurableWake json.RawMessage `json:"nanite_durable_wake,omitempty"`
}

func DurableWakeGrantFromIdentity(identity json.RawMessage) (DurableWakeGrant, error) {
	if len(identity) == 0 {
		return DurableWakeGrant{}, ErrDurableWakeNotGranted
	}
	if len(identity) > maxQueryScopeBytes {
		return DurableWakeGrant{}, fmt.Errorf("pluginapi: wake identity oversized")
	}
	var claims hostIdentity
	if err := manifest.DecodeExtension(identity, &claims); err != nil {
		return DurableWakeGrant{}, fmt.Errorf("pluginapi: invalid host identity")
	}
	if len(claims.DurableWake) == 0 {
		return DurableWakeGrant{}, ErrDurableWakeNotGranted
	}
	var grant DurableWakeGrant
	if err := manifest.DecodeExtension(claims.DurableWake, &grant); err != nil {
		return grant, fmt.Errorf("pluginapi: invalid wake grant")
	}
	return grant, grant.Validate()
}
func (grant DurableWakeGrant) Validate() error {
	if grant.Protocol != DurableWakeProtocol || !manifest.ValidID(grant.PluginID) || len(grant.PluginID) > 63 || len(grant.HostURL) > 1024 {
		return fmt.Errorf("pluginapi: invalid wake grant identity or protocol")
	}
	token, err := base64.RawURLEncoding.DecodeString(grant.Token)
	if err != nil || len(token) != 32 {
		return fmt.Errorf("pluginapi: invalid wake credential")
	}
	if _, err = loopbackHostURL(grant.HostURL); err != nil {
		return err
	}
	return grant.Scope.Validate()
}

// DurableWakeRequest carries a bounded prompt to the existing core wake
// service. Prompt delivery starts a real agent turn; plugins own translation.
type DurableWakeRequest struct {
	AgentSlug string            `json:"agent_slug"`
	Reason    string            `json:"reason"`
	Prompt    string            `json:"prompt"`
	Facts     map[string]string `json:"facts,omitempty"`
}

func (request DurableWakeRequest) Validate() error {
	if !agentSlug.MatchString(request.AgentSlug) || strings.TrimSpace(request.Prompt) == "" || len(request.Prompt) > 32768 || len(request.Reason) > 256 || len(request.Facts) > 32 {
		return fmt.Errorf("pluginapi: invalid wake request")
	}
	for _, text := range []string{request.Prompt, request.Reason} {
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return fmt.Errorf("pluginapi: invalid wake text")
		}
	}
	for key, value := range request.Facts {
		if strings.TrimSpace(key) == "" || len(key) > 128 || len(value) > 8192 || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("pluginapi: invalid wake facts")
		}
	}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > MaxDurableWakeBytes {
		return fmt.Errorf("pluginapi: wake request oversized")
	}
	return nil
}

// DurableWakeResponse returns only core identity and state, never an agent
// profile, credential, message history or model output.
type DurableWakeResponse struct {
	Protocol   int    `json:"protocol"`
	AgentSlug  string `json:"agent_slug"`
	InstanceID string `json:"instance_id"`
	SessionID  string `json:"session_id,omitempty"`
	Status     string `json:"status"`
}
type DurableWakeClient struct {
	grant    DurableWakeGrant
	endpoint string
	http     *http.Client
}

func NewDurableWakeClient(grant DurableWakeGrant, transport http.RoundTripper) (*DurableWakeClient, error) {
	if err := grant.Validate(); err != nil {
		return nil, err
	}
	client, err := NewClient(grant.HostURL, transport)
	if err != nil {
		return nil, err
	}
	grant.Scope.AgentSlugs = slices.Clone(grant.Scope.AgentSlugs)
	httpClient := *client.http
	httpClient.Timeout = 30 * time.Second
	return &DurableWakeClient{grant: grant, endpoint: strings.TrimSuffix(grant.HostURL, "/") + "/api/plugin-host/durable-wake", http: &httpClient}, nil
}

// Wake makes one request with no retries: retrying an uncertain result could
// start a second agent turn. The server checks scope and live connection again.
func (client *DurableWakeClient) Wake(ctx context.Context, call DurableWakeRequest) (DurableWakeResponse, error) {
	if err := call.Validate(); err != nil {
		return DurableWakeResponse{}, err
	}
	if !client.grant.Scope.Allows(call.AgentSlug) {
		return DurableWakeResponse{}, fmt.Errorf("pluginapi: wake exceeds granted scope")
	}
	raw, err := json.Marshal(call)
	if err != nil {
		return DurableWakeResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(raw))
	if err != nil {
		return DurableWakeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.grant.Token)
	resp, err := client.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return DurableWakeResponse{}, ctx.Err()
		}
		return DurableWakeResponse{}, fmt.Errorf("pluginapi: wake request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return DurableWakeResponse{}, fmt.Errorf("pluginapi: wake host returned HTTP %d", resp.StatusCode)
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, MaxDurableWakeBytes+1))
	if err != nil {
		return DurableWakeResponse{}, fmt.Errorf("pluginapi: wake response read failed")
	}
	if len(raw) > MaxDurableWakeBytes {
		return DurableWakeResponse{}, fmt.Errorf("pluginapi: wake response oversized")
	}
	var result DurableWakeResponse
	if err = manifest.DecodeExtension(raw, &result); err != nil {
		return result, fmt.Errorf("pluginapi: invalid wake response")
	}
	if result.Protocol != DurableWakeProtocol || result.AgentSlug != call.AgentSlug || !validQuerySession(result.InstanceID) || result.Status != "queued" || (result.SessionID != "" && !validQuerySession(result.SessionID)) {
		return DurableWakeResponse{}, fmt.Errorf("pluginapi: wake response does not match request")
	}
	return result, nil
}
