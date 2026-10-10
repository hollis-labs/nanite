// Package agentdefs carries pristine flat agentdef v2 artifacts. These files
// contain intrinsic behavior, never operator rows, host settings or grants.
package agentdefs

import (
	"embed"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

//go:embed definitions/*.md resources/*.md
var authored embed.FS

type Resource struct {
	URI     string
	Content []byte
}
type Artifact struct {
	Data      []byte
	Resources []Resource
}

func Catalog() ([]Artifact, error) {
	entries, err := authored.ReadDir("definitions")
	if err != nil {
		return nil, err
	}
	out := make([]Artifact, 0, len(entries))
	for _, entry := range entries {
		data, err := authored.ReadFile("definitions/" + entry.Name())
		if err != nil {
			return nil, err
		}
		d, err := agentdef.Parse(data, agentpolicy.Option())
		if err != nil {
			return nil, fmt.Errorf("authored definition %s: %w", entry.Name(), err)
		}
		a := Artifact{Data: data}
		for _, ref := range append(append([]agentdef.Ref{}, d.Behavior.Instructions...), d.Behavior.SOPs...) {
			body, err := authored.ReadFile(ref.URI)
			if err != nil {
				return nil, err
			}
			if agentdef.ArtifactDigest(body) != ref.Digest {
				return nil, fmt.Errorf("authored resource %s: digest mismatch", ref.URI)
			}
			a.Resources = append(a.Resources, Resource{URI: ref.URI, Content: body})
		}
		out = append(out, a)
	}
	return out, nil
}
