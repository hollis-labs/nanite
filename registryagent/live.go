package registryagent

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
	"strings"
	"time"

	"github.com/hollis-labs/nanite/toolregistry"
)

// LiveConfig selects a read-only Registry HTTP API. No endpoint or credential
// is inferred. Token is called for each request; its errors are not exposed.
// Authority names this resolver, not the record's source-of-truth authority.
type LiveConfig struct {
	URL         string
	Authority   string
	Publication string // published (default) or draft; never falls back
	Token       func(context.Context) (string, error)
	HTTP        *http.Client
}

// Live resolves Registry records through its versioned read-only HTTP API.
// A separate BlobStore supplies Caster bytes to ToolClient.
type Live struct {
	config LiveConfig
	client http.Client
}

func NewLive(c LiveConfig) (*Live, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || c.Authority == "" {
		return nil, fmt.Errorf("registryagent: invalid Registry endpoint or authority")
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("registryagent: HTTPS required outside loopback")
		}
	}
	if c.Publication == "" {
		c.Publication = "published"
	}
	if c.Publication != "draft" && c.Publication != "published" {
		return nil, fmt.Errorf("registryagent: publication must be published or draft")
	}
	client := http.Client{}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	// Never forward credentials to a redirect destination. A context deadline
	// also bounds clients supplied without a timeout.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Live{config: c, client: client}, nil
}

// ErrNotFound identifies an absent record or pinned revision.
var ErrNotFound = errors.New("registryagent: not found")

type liveRecord struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Schema      string          `json:"schema"`
	Revision    string          `json:"revision_id"`
	Publication string          `json:"publication"`
	Payload     json.RawMessage `json:"payload"`
}

func (l *Live) read(ctx context.Context, id, revision string) (liveRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	route := "/v1/record"
	args := map[string]string{"publication": l.config.Publication}
	if revision != "" {
		route = "/v1/revision"
		args = map[string]string{"revision": revision}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return liveRecord{}, err
	}
	endpoint := strings.TrimRight(l.config.URL, "/") + route + "?" + url.Values{"id": {id}, "args": {string(raw)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return liveRecord{}, fmt.Errorf("registryagent: invalid read request")
	}
	if l.config.Token != nil {
		token, err := l.config.Token(ctx)
		if err != nil || strings.ContainsAny(token, "\r\n") {
			return liveRecord{}, fmt.Errorf("registryagent: credential resolution failed")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := l.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return liveRecord{}, ctx.Err()
		}
		return liveRecord{}, fmt.Errorf("registryagent: Registry read failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return liveRecord{}, fmt.Errorf("%w: Registry read returned HTTP 404", ErrNotFound)
	}
	if response.StatusCode != http.StatusOK {
		return liveRecord{}, fmt.Errorf("registryagent: Registry read returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*maxRecordBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return liveRecord{}, ctx.Err()
		}
		return liveRecord{}, fmt.Errorf("registryagent: Registry response read failed")
	}
	if len(data) > 2*maxRecordBytes {
		return liveRecord{}, fmt.Errorf("registryagent: Registry response exceeds size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := readValue(dec, 0); err != nil {
		return liveRecord{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return liveRecord{}, fmt.Errorf("registryagent: trailing Registry JSON")
	}
	var record liveRecord
	if json.Unmarshal(data, &record) != nil || record.ID != id || record.Revision == "" || (revision != "" && record.Revision != revision) {
		return liveRecord{}, fmt.Errorf("registryagent: Registry identity or revision mismatch")
	}
	return record, nil
}

func (l *Live) resolve(ctx context.Context, kind, id, revision string) (liveRecord, error) {
	record, err := l.read(ctx, kind+":"+id, revision)
	if err != nil {
		return liveRecord{}, err
	}
	if record.Kind != kind || record.Schema != kind+"/1" {
		return liveRecord{}, fmt.Errorf("registryagent: Registry kind mismatch")
	}
	// Revision history stores authored drafts. Published mode only admits a
	// revision proven to be the current published record, never an unseen draft.
	if revision != "" && l.config.Publication == "published" {
		published, err := l.read(ctx, kind+":"+id, "")
		if err != nil {
			return liveRecord{}, err
		}
		if published.Revision != revision || !equalJSON(published.Payload, record.Payload) {
			return liveRecord{}, fmt.Errorf("registryagent: pinned revision is not currently published")
		}
		record.Publication = published.Publication
	}
	if record.Publication != l.config.Publication {
		return liveRecord{}, fmt.Errorf("registryagent: Registry publication mismatch")
	}
	payload, err := validate(ctx, record.Payload)
	if err != nil {
		return liveRecord{}, err
	}
	if payload["id"] != id || payload["schema"] != kind+"/1" {
		return liveRecord{}, fmt.Errorf("registryagent: Registry payload identity mismatch")
	}
	return record, nil
}

func (l *Live) Get(ctx context.Context, ref Reference, revision string) (Record, error) {
	if ref.Authority != l.config.Authority {
		return Record{}, fmt.Errorf("registryagent: reference authority does not match Registry")
	}
	record, err := l.resolve(ctx, ref.Kind, strings.TrimPrefix(ref.ID, ref.Kind+":"), revision)
	if err != nil {
		return Record{}, err
	}
	return Record{Reference: ref, Revision: record.Revision, Publication: record.Publication, Payload: record.Payload}, nil
}

// Publication reports the explicitly selected publication channel.
func (l *Live) Publication() string { return l.config.Publication }

// ToolClient connects read-only records to a host-supplied Caster blob reader.
func (l *Live) ToolClient(blobs toolregistry.BlobStore) toolregistry.Client {
	return toolregistry.Client{Records: l, Blobs: blobs}
}

func (*Live) Publish(context.Context, toolregistry.Record) error {
	return fmt.Errorf("registryagent: live adapter is read-only")
}

func (l *Live) Resolve(ctx context.Context, ref string) (toolregistry.Record, error) {
	id, ok := strings.CutPrefix(ref, "tool-package:")
	if !ok || id == "" {
		return toolregistry.Record{}, fmt.Errorf("registryagent: expected immutable tool-package reference")
	}
	record, err := l.resolve(ctx, "tool-package", id, "")
	if err != nil {
		return toolregistry.Record{}, err
	}
	var p struct {
		ID, Publisher, Name, Version, Digest, Artifact, Entry string
		Execution                                             struct{ Kind string }
		Needs                                                 []struct {
			Audience    string
			Scopes, Env []string
		}
		Env        []string
		Provenance toolregistry.Provenance
	}
	if err := json.Unmarshal(record.Payload, &p); err != nil {
		return toolregistry.Record{}, fmt.Errorf("registryagent: invalid package payload")
	}
	if p.ID != p.Publisher+"/"+p.Name+"@"+p.Version || !strings.HasSuffix(p.Provenance.Publisher, "/"+p.Publisher) {
		return toolregistry.Record{}, fmt.Errorf("registryagent: package publisher mismatch")
	}
	result := toolregistry.Record{ID: ref, Kind: "tool-package/1", Name: p.Name, Version: p.Version, Digest: p.Digest, Artifact: p.Artifact, Entry: p.Entry, Execution: p.Execution.Kind, Env: nonempty(p.Env), Provenance: p.Provenance}
	for _, need := range p.Needs {
		if len(need.Env) == 0 {
			return toolregistry.Record{}, fmt.Errorf("registryagent: package need requires an environment binding")
		}
		for _, env := range need.Env {
			result.Needs = append(result.Needs, toolregistry.Need{Audience: need.Audience, Scopes: nonempty(need.Scopes), Env: env})
		}
	}
	return result, nil
}

func nonempty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

// Normalize object key order and whitespace without rounding numeric values.
func equalJSON(a, b []byte) bool {
	canonical := func(raw []byte) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("invalid JSON tail")
		}
		return json.Marshal(value)
	}
	left, err := canonical(a)
	if err != nil {
		return false
	}
	right, err := canonical(b)
	return err == nil && bytes.Equal(left, right)
}
