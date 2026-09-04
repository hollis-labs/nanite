package envelope

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hollis-labs/go-envelopes"
)

// envelopeRegistry is the shared go-envelopes Registry installed at the
// composition root through envelopewiring.InstallSharedRegistry.
var (
	envelopeRegistryMu sync.RWMutex
	envelopeRegistry   *envelopes.Registry
)

// SetEnvelopeRegistry installs the shared registry used for schema lookup and
// validation. Passing nil unsets it for composition-root failure tests.
func SetEnvelopeRegistry(r *envelopes.Registry) {
	envelopeRegistryMu.Lock()
	envelopeRegistry = r
	envelopeRegistryMu.Unlock()
}

// EnvelopeRegistry returns the currently installed registry, which may be nil.
func EnvelopeRegistry() *envelopes.Registry {
	envelopeRegistryMu.RLock()
	defer envelopeRegistryMu.RUnlock()
	return envelopeRegistry
}

// PassiveRenderableTypes is Nanite's v1 allow-list for card_show. It is host
// presentation policy; schema ownership stays with go-envelopes.
var PassiveRenderableTypes = []string{
	"document-viewer",
	"report-card",
	"info-card",
	"list-card",
	"metric-card",
	"progress-card",
	"table-card",
	"timeline-card",
	"diff-card",
	"artifact-mini",
}

var passiveRenderableSet = func() map[string]bool {
	set := make(map[string]bool, len(PassiveRenderableTypes))
	for _, envelopeType := range PassiveRenderableTypes {
		set[envelopeType] = true
	}
	return set
}()

// IsPassiveRenderable reports whether envelopeType is addressable through
// Nanite's card_show presentation path.
func IsPassiveRenderable(envelopeType string) bool {
	return passiveRenderableSet[envelopeType]
}

func registrySpec(envelopeType string) (*envelopes.Registry, envelopes.TypeSpec, error) {
	registry := EnvelopeRegistry()
	if registry == nil {
		return nil, envelopes.TypeSpec{}, fmt.Errorf("envelope registry not configured (call SetEnvelopeRegistry at startup)")
	}
	if spec, ok := registry.Lookup(envelopeType); ok && spec.DataSchema != nil {
		return registry, spec, nil
	}
	legacyName := LegacyTypeName(envelopeType)
	if spec, ok := registry.Lookup(legacyName); ok && spec.DataSchema != nil {
		return registry, spec, nil
	}
	return nil, envelopes.TypeSpec{}, fmt.Errorf("no schema registered for envelope type %q", envelopeType)
}

// DefaultRenderTarget returns Nanite's interpretation of the schema extension
// annotation default_render_target. go-envelopes owns schema traversal and
// annotation extraction; Nanite owns the meaning of this host-specific hint.
func DefaultRenderTarget(envelopeType string) string {
	_, spec, err := registrySpec(envelopeType)
	if err != nil || spec.DataSchemaDocument == nil {
		return ""
	}
	target, _ := spec.DataSchemaDocument.Metadata().Custom["default_render_target"].(string)
	return target
}

// ValidateData validates a card payload through the shared go-envelopes
// registry. The returned validation error is the module's bounded, structured
// error; its raw Inner value remains available for existing classifiers but
// must not be logged because the underlying validator may include input data.
func ValidateData(envelopeType string, data any) error {
	registry, spec, err := registrySpec(envelopeType)
	if err != nil {
		return err
	}
	payload, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("envelope %q data must be a JSON object, got %T", envelopeType, data)
	}
	return registry.ValidateEnvelope(&envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "nanite-schema-validation",
		Type: spec.Name,
		Data: payload,
	})
}

// ValidationError retains Nanite's existing type name while making the
// go-envelopes error contract authoritative.
type ValidationError = envelopes.ValidationError

// StructuredError is Nanite's compact agent-facing presentation of one
// module-owned ValidationFailure.
type StructuredError struct {
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion,omitempty"`
}

// FlattenSchemaError adapts go-envelopes' structured failure details into the
// compact shape used by Nanite tools. schemaDoc is retained only for source
// compatibility; schema traversal and metadata lookup are module-owned now.
func FlattenSchemaError(err error, _ map[string]any) []StructuredError {
	if err == nil {
		return nil
	}

	var validationError *envelopes.ValidationError
	var failures []envelopes.ValidationFailure
	if errors.As(err, &validationError) {
		failures = validationError.Details()
	} else {
		failures = envelopes.FlattenValidationFailures(err, nil)
	}
	if len(failures) == 0 {
		return []StructuredError{{Reason: err.Error()}}
	}

	out := make([]StructuredError, 0, len(failures))
	for _, failure := range failures {
		out = append(out, presentValidationFailure(failure))
	}
	return out
}

func presentValidationFailure(failure envelopes.ValidationFailure) StructuredError {
	reason := failure.Message
	suggestion := ""
	switch failure.Keyword {
	case "required":
		missing := stringsFromExpected(failure.Expected)
		if len(missing) > 0 {
			reason += ": " + joinQuoted(missing)
			if len(missing) == 1 {
				suggestion = "add required field " + quote(missing[0])
			} else {
				suggestion = "add required fields " + joinQuoted(missing)
			}
		}
	case "additionalProperties":
		allowed := stringsFromExpected(failure.Expected)
		if len(allowed) == 0 && failure.Metadata != nil {
			allowed = append([]string(nil), failure.Metadata.Properties...)
		}
		if len(allowed) > 0 {
			suggestion = "allowed: " + strings.Join(allowed, ", ")
		}
	case "type":
		want := stringsFromExpected(failure.Expected)
		got := ""
		if failure.Actual != nil {
			got = failure.Actual.Type
		}
		if got != "" && len(want) > 0 {
			reason = fmt.Sprintf("got %s, want %s", got, strings.Join(want, " or "))
		}
		if len(want) == 1 && want[0] == "array" {
			if got == "object" {
				suggestion = "wrap in `[...]`"
			} else {
				suggestion = "wrap the value in an array: `[...]`"
			}
		} else if len(want) == 1 && want[0] == "string" && (got == "number" || got == "integer" || got == "boolean") {
			suggestion = "stringify the value (use quotes)"
		}
	case "enum":
		allowed := stringsFromExpected(failure.Expected)
		if len(allowed) > 0 {
			suggestion = "allowed values: " + strings.Join(allowed, ", ")
		}
	}
	return StructuredError{Path: failure.InstancePath, Reason: reason, Suggestion: suggestion}
}

func stringsFromExpected(expected any) []string {
	values, ok := expected.([]string)
	if ok {
		return append([]string(nil), values...)
	}
	items, ok := expected.([]any)
	if !ok {
		return nil
	}
	values = make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			values = append(values, value)
		}
	}
	return values
}

func quote(value string) string { return "'" + value + "'" }

func joinQuoted(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quote(value))
	}
	return strings.Join(quoted, ", ")
}

// ValidateAgainstSchema validates arbitrary tool arguments while delegating
// schema normalization, compilation, metadata extraction, and structured
// failure construction to go-envelopes.
func ValidateAgainstSchema(schema map[string]any, value any) ([]StructuredError, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema is nil")
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	document, err := envelopes.NewSchemaDocument("mem://nanite/tool-input/inline.schema.json", raw)
	if err != nil {
		return nil, err
	}
	registry := envelopes.NewRegistry()
	const typeName = "nanite.inline-tool-input"
	if registerErr := registry.RegisterType(envelopes.TypeSpec{
		Name:               typeName,
		Source:             envelopes.TypeSourcePlugin,
		PluginID:           "nanite",
		DataSchemaDocument: document,
	}); registerErr != nil {
		return nil, fmt.Errorf("compile schema: %w", registerErr)
	}

	payload, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool input must be a JSON object, got %T", value)
	}
	err = registry.ValidateEnvelope(&envelopes.Envelope{
		V:    envelopes.ProtocolVersion,
		ID:   "nanite-tool-input-validation",
		Type: typeName,
		Data: payload,
	})
	if err == nil {
		return nil, nil
	}
	if !errors.Is(err, envelopes.ErrSchemaValidation) {
		return nil, err
	}
	return FlattenSchemaError(err, nil), nil
}

// ValidateEnvelopeData returns Nanite's compact agent-facing failures for an
// envelope payload while retaining registry/configuration errors separately.
func ValidateEnvelopeData(envelopeType string, data any) ([]StructuredError, error) {
	err := ValidateData(envelopeType, data)
	if err == nil {
		return nil, nil
	}
	if !errors.Is(err, envelopes.ErrSchemaValidation) {
		return nil, err
	}
	return FlattenSchemaError(err, nil), nil
}
