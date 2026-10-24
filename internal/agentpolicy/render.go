package agentpolicy

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
)

// FilesRoot is the directory, relative to a session's root (a CLI boot root or
// an API session workspace), under which a definition's pinned files are
// planted.
const FilesRoot = "agent"

// ErrRenderContent marks a pinned document whose bytes do not match its pin.
var ErrRenderContent = errors.New("definition resource does not match its pin")

// Rendered is what a definition contributes to a launch: the instruction text
// and the files planted beside it. Both the native loop and the CLI runtimes
// consume the same rendering.
type Rendered struct {
	Instructions string
	// Files maps a path relative to the session root (always under FilesRoot)
	// to its bytes. Empty when the definition pins no files.
	Files map[string][]byte
}

// Render builds the instruction content in order: purpose, body, instructions,
// SOPs, the completion criterion, then steering documents. Reference files
// (requirements.resources and harness_profile.context.sources) are returned in
// Files and the context sources are listed in the instructions. Every document
// is verified against its pin.
func Render(d *agentdef.Definition, read func(agentdef.Ref) ([]byte, error)) (Rendered, error) {
	var out Rendered
	if d == nil {
		return out, errors.New("render: empty definition")
	}
	fetch := func(ref agentdef.Ref) ([]byte, error) {
		if read == nil {
			return nil, fmt.Errorf("resource %q: no resolver", ref.URI)
		}
		body, err := read(ref)
		if err != nil {
			return nil, err
		}
		if agentdef.ArtifactDigest(body) != ref.Digest {
			return nil, fmt.Errorf("%w: %q", ErrRenderContent, ref.URI)
		}
		return body, nil
	}
	text := d.Behavior.Purpose + "\n\n" + d.Body
	for _, refs := range [][]agentdef.Ref{d.Behavior.Instructions, d.Behavior.SOPs} {
		for _, ref := range refs {
			body, err := fetch(ref)
			if err != nil {
				return out, err
			}
			text += "\n\n" + string(body)
		}
	}
	if d.Behavior.Completion != "" {
		text += "\n\nCompletion: " + d.Behavior.Completion
	}
	for _, ref := range d.HarnessProfile.Steering {
		body, err := fetch(ref)
		if err != nil {
			return out, err
		}
		text += "\n\n" + string(body)
	}

	files := map[string][]byte{}
	digests := map[string]string{}
	plant := func(ref agentdef.Ref) (string, error) {
		body, err := fetch(ref)
		if err != nil {
			return "", err
		}
		p := plantPath(ref)
		if prior, taken := digests[p]; taken && prior != ref.Digest {
			p = path.Join(FilesRoot, "ref-"+strings.TrimPrefix(ref.Digest, "sha256:")[:12])
		}
		digests[p] = ref.Digest
		files[p] = body
		return p, nil
	}
	for _, ref := range d.Requirements.Resources {
		if _, err := plant(ref); err != nil {
			return out, err
		}
	}
	var listed []string
	for _, ref := range d.HarnessProfile.Context.Sources {
		p, err := plant(ref)
		if err != nil {
			return out, err
		}
		listed = append(listed, p)
	}
	if len(listed) > 0 {
		text += "\n\nReference files, relative to your session root (read them when relevant):"
		for _, p := range listed {
			text += "\n- " + p
		}
	}
	out.Instructions = text
	if len(files) > 0 {
		out.Files = files
	}
	return out, nil
}

// plantPath is where a pinned file lands: its URI when that is a relative path
// that stays inside the files root, else a name derived from its digest.
func plantPath(ref agentdef.Ref) string {
	rel := path.Clean(strings.TrimPrefix(ref.URI, "file:"))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") || strings.Contains(rel, ":") || strings.Contains(rel, "\\") {
		return path.Join(FilesRoot, "ref-"+strings.TrimPrefix(ref.Digest, "sha256:")[:12])
	}
	return path.Join(FilesRoot, rel)
}
