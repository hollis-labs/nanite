package envelope

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaFiles embeds the per-type JSON Schema definitions so the runtime
// validator does not depend on a filesystem layout outside the binary.
//
//go:embed schemas/*.json
var schemaFiles embed.FS

// PassiveRenderableTypes is the v1 allow-list of envelope types the agent
// may emit through nanite_show_card. These are the cards that carry no
// decision-flow / runtime state — the agent decides "use a card with this
// data", the system decides where it lands. Decision-flow envelopes
// (approval-card, proposal-card, confirmation-card, question-form),
// runtime-emitted envelopes (chat-loop-terminated, elicitation-prompt,
// subagent-spawn-approval), and plugin-shipped envelopes (kb-result,
// ticket-*) intentionally have their own emission paths and are not
// addressable through show_card.
//
// Source of truth: SP-20260428-0001 / CW-20260428-0019 (Collab UI v1, A3).
var PassiveRenderableTypes = []string{
	"giphy-modal",
	"document-viewer",
	"report-card",
	"info-card",
	"list-card",
	"metric-card",
	"progress-card",
	"table-card",
	"timeline-card",
	"diff-card",
}

// passiveRenderableSet is the allow-list as a set for O(1) membership checks.
var passiveRenderableSet = func() map[string]bool {
	m := make(map[string]bool, len(PassiveRenderableTypes))
	for _, t := range PassiveRenderableTypes {
		m[t] = true
	}
	return m
}()

// IsPassiveRenderable reports whether the given envelope type is on the v1
// agent-emittable allow-list.
func IsPassiveRenderable(envelopeType string) bool {
	return passiveRenderableSet[envelopeType]
}

// schemaCache memoises compiled schemas so each tool call doesn't re-parse
// the schema files. Compilation is cheap relative to validation but a chat
// session can fire dozens of show_card calls per turn, so reuse pays off
// quickly.
var (
	schemaCacheMu sync.Mutex
	schemaCache   = map[string]*jsonschema.Schema{}
)

// loadSchema returns a compiled schema for the given envelope type, lazily
// compiling on first use. Errors carry the type name so the caller's error
// message stays informative.
func loadSchema(envelopeType string) (*jsonschema.Schema, error) {
	schemaCacheMu.Lock()
	defer schemaCacheMu.Unlock()
	if cached, ok := schemaCache[envelopeType]; ok {
		return cached, nil
	}
	path := "schemas/" + envelopeType + ".schema.json"
	raw, err := schemaFiles.ReadFile(path)
	if err != nil {
		if errIsNotExist(err) {
			return nil, fmt.Errorf("no schema registered for envelope type %q", envelopeType)
		}
		return nil, fmt.Errorf("read schema for %q: %w", envelopeType, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse schema for %q: %w", envelopeType, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(envelopeType+".schema.json", doc); err != nil {
		return nil, fmt.Errorf("register schema for %q: %w", envelopeType, err)
	}
	compiled, err := compiler.Compile(envelopeType + ".schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile schema for %q: %w", envelopeType, err)
	}
	schemaCache[envelopeType] = compiled
	return compiled, nil
}

// errIsNotExist returns true for fs.ErrNotExist (fs.ReadFile wraps it).
func errIsNotExist(err error) bool {
	return err != nil && (err == fs.ErrNotExist || strings.Contains(err.Error(), "file does not exist"))
}

// ValidateData validates a card payload against the registered schema for
// envelopeType. data is the envelope's `data` field as parsed JSON — usually
// a map[string]any from the tool args. Returns nil on success or a
// ValidationError describing the failure (location + reason) when the
// payload does not match the schema. Returns a plain error if envelopeType
// is unknown or the schema cannot be loaded.
func ValidateData(envelopeType string, data any) error {
	schema, err := loadSchema(envelopeType)
	if err != nil {
		return err
	}
	if err := schema.Validate(data); err != nil {
		return &ValidationError{Type: envelopeType, Inner: err}
	}
	return nil
}

// ValidationError wraps a jsonschema validation failure with the envelope
// type that triggered it. Callers can either use err.Error() to surface a
// human-readable message or unwrap to inspect the underlying
// *jsonschema.ValidationError tree.
type ValidationError struct {
	Type  string
	Inner error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("envelope %q failed schema validation: %v", e.Type, e.Inner)
}

func (e *ValidationError) Unwrap() error { return e.Inner }
