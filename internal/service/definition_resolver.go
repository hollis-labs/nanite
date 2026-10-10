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

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
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
	Ref          DefinitionRef
	Definition   *agentdef.Definition
	ReadResource func(context.Context, agentdef.Ref) ([]byte, error)
}
type DefinitionResolver interface {
	Resolve(context.Context, DefinitionRef) (VerifiedDefinition, error)
}

//go:embed definitions/default.md
var embeddedChatDefinition []byte

func EmbeddedDefinition() (VerifiedDefinition, error) {
	d, err := agentdef.Parse(embeddedChatDefinition, agentpolicy.Option())
	if err != nil {
		return VerifiedDefinition{}, err
	}
	digest, err := agentdef.Digest(d)
	if err != nil {
		return VerifiedDefinition{}, err
	}
	return VerifiedDefinition{Ref: DefinitionRef{d.DefinitionID, d.Revision, digest}, Definition: d}, nil
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
		d, err := agentdef.Parse(data, agentpolicy.Option())
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
		entries[key] = VerifiedDefinition{Ref: DefinitionRef{d.DefinitionID, d.Revision, digest}, Definition: d}
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
	Instructions      string                    `json:"instructions"`
	PermissionProfile string                    `json:"permission_profile"`
	Model             ModelSelection            `json:"model"`
	NativePolicy      *agentpolicy.NativePolicy `json:"native_policy,omitempty"`
	ReflexBundle      *agentpolicy.ReflexBundle `json:"reflex_bundle,omitempty"`
	HostSettings      *store.NativeHostSettings `json:"host_settings,omitempty"`
}

func MapChatDefinition(verified VerifiedDefinition) (ChatDefinitionConfig, error) {
	return mapChatDefinition(context.Background(), verified)
}

func mapChatDefinition(ctx context.Context, verified VerifiedDefinition) (ChatDefinitionConfig, error) {
	d := verified.Definition
	if d == nil {
		return ChatDefinitionConfig{}, ErrUnsupportedDefinition
	}
	if err := agentpolicy.ValidateExecution(d); err != nil {
		return ChatDefinitionConfig{}, fmt.Errorf("%w: %w", ErrUnsupportedDefinition, err)
	}
	p := d.HarnessProfile.Permissions.Profile
	var native *agentpolicy.NativePolicy
	if e, ok := d.Extensions[agentpolicy.NativeNamespace]; ok {
		policy, err := agentpolicy.DecodeNative(e)
		if err != nil {
			return ChatDefinitionConfig{}, fmt.Errorf("%w: %w", ErrUnsupportedDefinition, err)
		}
		policy = policy.Defaults()
		native = &policy
	}
	var bundle *agentpolicy.ReflexBundle
	if e, ok := d.Extensions[agentpolicy.ReflexNamespace]; ok {
		if verified.ReadResource == nil {
			return ChatDefinitionConfig{}, fmt.Errorf("%w: reflex resource resolver unavailable", ErrUnsupportedDefinition)
		}
		p, err := agentpolicy.DecodeReflex(e)
		if err != nil {
			return ChatDefinitionConfig{}, err
		}
		body, err := verified.ReadResource(ctx, p.Bundle)
		if err != nil {
			return ChatDefinitionConfig{}, err
		}
		b, err := agentpolicy.ParseReflexBundle(body, p.Bundle)
		if err != nil {
			return ChatDefinitionConfig{}, err
		}
		for _, rule := range b.Rules {
			if rule.Action.Kind == "force_tool_choice" {
				return ChatDefinitionConfig{}, fmt.Errorf("%w: tool preference requires verified actor grants", store.ErrVerifiedActorRequired)
			}
		}
		bundle = &b
	}
	// Unknown optional extensions are preserved by the resolver/digest. They are
	// neither negotiated host policy nor an authority source. Mandatory ones fail Validate.
	instructions := d.Behavior.Purpose + "\n\n" + d.Body
	for _, refs := range [][]agentdef.Ref{d.Behavior.Instructions, d.Behavior.SOPs} {
		for _, ref := range refs {
			if verified.ReadResource == nil {
				return ChatDefinitionConfig{}, fmt.Errorf("%w: instruction resource resolver unavailable", ErrUnsupportedDefinition)
			}
			body, err := verified.ReadResource(ctx, ref)
			if err != nil {
				return ChatDefinitionConfig{}, err
			}
			if agentdef.ArtifactDigest(body) != ref.Digest {
				return ChatDefinitionConfig{}, ErrDefinitionDigestMismatch
			}
			instructions += "\n\n" + string(body)
		}
	}
	if d.Behavior.Completion != "" {
		instructions += "\n\nCompletion: " + d.Behavior.Completion
	}
	return ChatDefinitionConfig{Instructions: instructions, PermissionProfile: p, NativePolicy: native, ReflexBundle: bundle}, nil
}
