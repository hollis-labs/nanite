// Package registryagent materializes Registry definitions into ordinary agent
// folders. Resolution happens before opening an agent, never in its runtime.
package registryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/hollis-labs/libs/security/redact"
)

// Reference preserves an authority's native identity. Revision is supplied
// separately to Resolver.Get; empty requests the current record.
type Reference struct {
	Authority string `json:"authority"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	URI       string `json:"uri,omitempty"`
}

// Record is a resolved immutable revision and its authored Registry payload.
type Record struct {
	Reference Reference       `json:"reference"`
	Revision  string          `json:"revision"`
	Payload   json.RawMessage `json:"payload"`
}

// Resolver returns an exact reference and, when supplied, exact revision.
type Resolver interface {
	Get(context.Context, Reference, string) (Record, error)
}

// Fake resolves an in-memory list. Put different revisions in the list; the
// last matching record is current when no revision is requested.
type Fake []Record

func (f Fake) Get(ctx context.Context, ref Reference, revision string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	for i := len(f) - 1; i >= 0; i-- {
		r := f[i]
		if sameRef(r.Reference, ref) && (revision == "" || r.Revision == revision) {
			r.Payload = append(json.RawMessage(nil), r.Payload...)
			return r, nil
		}
	}
	return Record{}, fmt.Errorf("registryagent: reference or revision not found")
}

func sameRef(a, b Reference) bool {
	return a.Authority == b.Authority && a.Kind == b.Kind && a.ID == b.ID
}

// Local is a development-only filesystem resolver. It has no authentication.
// records.json contains an array of Record values, bounded to 16 MiB.
type Local struct{ Dir string }

func (l Local) Get(ctx context.Context, ref Reference, revision string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	root, err := os.OpenRoot(l.Dir)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open("records.json")
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 16<<20+1))
	if err != nil {
		return Record{}, err
	}
	if len(data) > 16<<20 {
		return Record{}, fmt.Errorf("registryagent: local records exceed size limit")
	}
	var records Fake
	decErr := json.Unmarshal(data, &records)
	if decErr != nil {
		return Record{}, fmt.Errorf("registryagent: invalid local record envelope")
	}
	return records.Get(ctx, ref, revision)
}

type versioned struct {
	Reference Reference `json:"reference"`
	Revision  string    `json:"revision"`
}

// Source identifies one materialized input and its content digest.
type Source struct {
	Reference Reference `json:"reference"`
	Revision  string    `json:"revision"`
	Digest    string    `json:"digest"`
}

func fetch(ctx context.Context, r Resolver, ref Reference, revision string) (Record, map[string]any, Source, error) {
	if ref.Authority == "" || ref.Kind == "" || ref.ID == "" {
		return Record{}, nil, Source{}, fmt.Errorf("registryagent: incomplete reference")
	}
	record, err := r.Get(ctx, ref, revision)
	if err != nil {
		return Record{}, nil, Source{}, err
	}
	if !sameRef(record.Reference, ref) || record.Revision == "" || (revision != "" && record.Revision != revision) {
		return Record{}, nil, Source{}, fmt.Errorf("registryagent: resolver returned a different identity or revision")
	}
	for _, text := range []string{ref.Authority, ref.Kind, ref.ID, ref.URI, record.Revision} {
		if len(text) > 4096 || len(redact.Default().Scan(text)) > 0 {
			return Record{}, nil, Source{}, fmt.Errorf("registryagent: invalid or credential-like reference metadata")
		}
	}
	payload, err := validate(ctx, record.Payload)
	if err != nil {
		return Record{}, nil, Source{}, err
	}
	if !reflect.DeepEqual(payload["id"], ref.ID) || payload["schema"] != ref.Kind+"/1" {
		return Record{}, nil, Source{}, fmt.Errorf("registryagent: payload identity mismatch")
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return Record{}, nil, Source{}, err
	}
	sum := sha256.Sum256(canonical)
	return record, payload, Source{ref, record.Revision, hex.EncodeToString(sum[:])}, nil
}
