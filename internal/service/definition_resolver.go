package service

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	mesh "github.com/hollis-labs/substrate/mesh"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// DefinitionRef maps the API spelling to mesh DefinitionRef's ID, Revision,
// and Digest. SemanticDigest is the agentdef semantic hash, not artifact bytes.
type DefinitionRef struct {
	DefinitionID   string `json:"definition_id"`
	Revision       string `json:"revision"`
	SemanticDigest string `json:"semantic_digest"`
}

func (p DefinitionRef) MeshRef() mesh.DefinitionRef {
	return mesh.DefinitionRef{ID: p.DefinitionID, Revision: p.Revision, Digest: p.SemanticDigest}
}
func DefinitionRefFromMesh(p mesh.DefinitionRef) DefinitionRef {
	return DefinitionRef{p.ID, p.Revision, p.Digest}
}

var definitionDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (p DefinitionRef) Validate() error {
	if strings.TrimSpace(p.DefinitionID) == "" || strings.TrimSpace(p.Revision) == "" || !definitionDigestPattern.MatchString(p.SemanticDigest) {
		return errors.New("definition_ref requires definition_id, revision and sha256 semantic_digest")
	}
	return nil
}

var ErrDefinitionNotFound = errors.New("definition revision not found")
var ErrDefinitionDigestMismatch = errors.New("definition semantic digest mismatch")
var ErrUnsupportedDefinition = errors.New("definition semantics unsupported by native chat")
var ErrUnsupportedModel = errors.New("model selection is not authorized for native chat")

// VerifiedDefinition is host-resolved content. The resolver owns content/pin
// verification; the chat consumer checks identity and maps supported semantics.
type VerifiedDefinition struct {
	Ref        DefinitionRef
	Definition *agentdef.Definition
}
type DefinitionResolver interface {
	Resolve(context.Context, DefinitionRef) (VerifiedDefinition, error)
}

//go:embed definitions/default.md
var embeddedChatDefinition []byte

func EmbeddedDefinition() (VerifiedDefinition, error) {
	d, err := agentdef.Parse(embeddedChatDefinition)
	if err != nil {
		return VerifiedDefinition{}, err
	}
	digest, err := agentdef.Digest(d)
	if err != nil {
		return VerifiedDefinition{}, err
	}
	return VerifiedDefinition{DefinitionRef{d.DefinitionID, d.Revision, digest}, d}, nil
}

// FileDefinitionResolver reads a host-selected directory. Request identifiers
// never become paths. Symlinks and nonregular definition files are refused.
// Files are reread on resolution, so a changed revision cannot satisfy an old pin.
type FileDefinitionResolver struct{ Directory string }

func NewFileDefinitionResolver(directory string) (*FileDefinitionResolver, error) {
	r := &FileDefinitionResolver{Directory: directory}
	_, err := r.catalog(context.Background())
	return r, err
}
func (r *FileDefinitionResolver) catalog(ctx context.Context) (map[string]VerifiedDefinition, error) {
	base, err := EmbeddedDefinition()
	if err != nil {
		return nil, err
	}
	entries := map[string]VerifiedDefinition{base.Ref.DefinitionID + "\x00" + base.Ref.Revision: base}
	if r.Directory == "" {
		return entries, nil
	}
	files, err := os.ReadDir(r.Directory)
	if err != nil {
		return nil, fmt.Errorf("read agentdef directory: %w", err)
	}
	for _, f := range files {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		if !f.Type().IsRegular() {
			return nil, fmt.Errorf("agentdef file %q must be regular", f.Name())
		}
		path := filepath.Join(r.Directory, f.Name())
		file, err := os.Open(path) // #nosec G304 -- regular catalog entry under the explicitly configured host directory; no request-derived path.
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > 1<<20 {
			return nil, errors.New("agentdef file exceeds 1 MiB")
		}
		d, err := agentdef.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("agentdef %q: %w", f.Name(), err)
		}
		digest, err := agentdef.Digest(d)
		if err != nil {
			return nil, err
		}
		key := d.DefinitionID + "\x00" + d.Revision
		if _, exists := entries[key]; exists {
			return nil, fmt.Errorf("duplicate definition revision %q", d.DefinitionID)
		}
		entries[key] = VerifiedDefinition{DefinitionRef{d.DefinitionID, d.Revision, digest}, d}
	}
	return entries, nil
}
func (r *FileDefinitionResolver) Resolve(ctx context.Context, pin DefinitionRef) (VerifiedDefinition, error) {
	if err := pin.Validate(); err != nil {
		return VerifiedDefinition{}, err
	}
	entries, err := r.catalog(ctx)
	if err != nil {
		return VerifiedDefinition{}, err
	}
	d, ok := entries[pin.DefinitionID+"\x00"+pin.Revision]
	if !ok {
		return VerifiedDefinition{}, ErrDefinitionNotFound
	}
	if d.Ref.SemanticDigest != pin.SemanticDigest {
		return VerifiedDefinition{}, ErrDefinitionDigestMismatch
	}
	return d, nil
}

// ChatDefinitionConfig contains only the applied chat-loop semantics. It is
// stored separately from caller metadata, which confers no host authority.
type ChatDefinitionConfig struct {
	Instructions      string         `json:"instructions"`
	PermissionProfile string         `json:"permission_profile"`
	Model             ModelSelection `json:"model"`
}

func MapChatDefinition(verified VerifiedDefinition) (ChatDefinitionConfig, error) {
	d := verified.Definition
	if d == nil {
		return ChatDefinitionConfig{}, ErrUnsupportedDefinition
	}
	if err := d.Validate(); err != nil {
		return ChatDefinitionConfig{}, fmt.Errorf("%w: %w", ErrUnsupportedDefinition, err)
	}
	if len(d.Behavior.Instructions)+len(d.Behavior.SOPs)+len(d.Behavior.Hooks)+len(d.Capabilities)+len(d.Requirements.Requires)+len(d.Requirements.Uses)+len(d.Requirements.Tools)+len(d.Requirements.Skills)+len(d.Requirements.Resources)+len(d.HarnessProfile.Steering)+len(d.HarnessProfile.Context.Sources)+len(d.HarnessProfile.Approvals)+len(d.HarnessProfile.Escalation) > 0 || d.HarnessProfile.Context.Policy != nil || d.Continuity.Mode != agentdef.Ephemeral || d.Continuity.MemoryPolicy != nil || d.Continuity.RecoveryStrategy != nil {
		return ChatDefinitionConfig{}, fmt.Errorf("%w: references, hooks, capability requests and continuity policies are not applied", ErrUnsupportedDefinition)
	}
	p := d.HarnessProfile.Permissions.Profile
	if p != "default" && p != "read-only" {
		return ChatDefinitionConfig{}, fmt.Errorf("%w: permission profile %q", ErrUnsupportedDefinition, p)
	}
	// Unknown optional extensions are preserved by the resolver/digest. They are
	// neither negotiated host policy nor an authority source. Mandatory ones fail Validate.
	instructions := d.Behavior.Purpose + "\n\n" + d.Body
	if d.Behavior.Completion != "" {
		instructions += "\n\nCompletion: " + d.Behavior.Completion
	}
	return ChatDefinitionConfig{Instructions: instructions, PermissionProfile: p}, nil
}
