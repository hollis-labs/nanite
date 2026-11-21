// Package toolregistry implements Nanite's tool-package/1 registry boundary.
// Registry owns the record contract; adapters provide records and CAS blobs
// separately. This package does not implement the live service transport.
package toolregistry

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Registry stores immutable package records, not artifact bytes.
// A live adapter creates draft tool-package/1 records and resolves their IDs.
type Registry interface {
	Publish(context.Context, Record) error
	Resolve(context.Context, string) (Record, error)
}

// BlobStore uploads and fetches deterministic tar bytes by cas://sha256/<hex>.
// Adapters must honor cancellation and bound response sizes. Install checks the
// returned bytes against both this content address and the package digest.
type BlobStore interface {
	Put(context.Context, []byte) (string, error)
	Fetch(context.Context, string) ([]byte, error)
}

// Client connects the separately owned record and artifact stores.
type Client struct {
	Records Registry
	Blobs   BlobStore
}

// Need describes token names and scopes, never their values.
type Need struct {
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes,omitempty"`
	Env      string   `json:"env"`
}

// Record describes a tool-package/1 record. ID names an opaque Tether publisher
// ID and an immutable package version. The artifact's tool.yaml is authoritative.
type Record struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	Digest     string     `json:"digest"`
	Artifact   string     `json:"artifact"`
	Entry      string     `json:"entry"`
	Execution  string     `json:"execution"`
	Needs      []Need     `json:"needs,omitempty"`
	Env        []string   `json:"env,omitempty"`
	Provenance Provenance `json:"provenance"`
}

// Provenance records the publisher's claimed Tether URN. Local fakes do not
// authenticate it; live adapters must apply the registry's publication policy.
type Provenance struct {
	Publisher string `json:"publisher"`
	SourceRef string `json:"source_ref,omitempty"`
}

// Ref is the immutable version reference for this record.
func (r Record) Ref() string { return r.ID }

var partPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var publisherPattern = regexp.MustCompile(`^agt_[a-z0-9]{1,64}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var artifactPattern = regexp.MustCompile(`^cas://sha256/[0-9a-f]{64}$`)

func publisherID(urn string) (string, error) {
	u, err := url.Parse(urn)
	if err != nil || u.Scheme != "msg" || u.Host != "agent" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") {
		return "", fmt.Errorf("publisher must be a Tether agent URN")
	}
	id := path.Base(u.Path)
	if !publisherPattern.MatchString(id) {
		return "", fmt.Errorf("publisher URN must end in an opaque agt id")
	}
	return id, nil
}

func (r Record) validate() error {
	publisher, err := publisherID(r.Provenance.Publisher)
	if err != nil {
		return err
	}
	if r.Kind != "tool-package/1" || r.ID != "tool-package:"+publisher+"/"+r.Name+"@"+r.Version || !partPattern.MatchString(r.Name) || !partPattern.MatchString(r.Version) || !digestPattern.MatchString(r.Digest) || !artifactPattern.MatchString(r.Artifact) || (r.Execution != "command" && r.Execution != "mcp") {
		return fmt.Errorf("invalid tool-package/1 registry record")
	}
	return nil
}

func matchesRef(r Record, ref string) bool { return r.ID == ref }
func validRef(ref string) bool {
	prefix, version, ok := strings.Cut(strings.TrimPrefix(ref, "tool-package:"), "@")
	publisher, name, split := strings.Cut(prefix, "/")
	return strings.HasPrefix(ref, "tool-package:") && ok && split && publisherPattern.MatchString(publisher) && partPattern.MatchString(name) && partPattern.MatchString(version)
}
