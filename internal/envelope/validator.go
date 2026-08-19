package envelope

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/hollis-labs/go-envelopes"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// envelopeRegistry is the shared go-envelopes Registry, set once at
// composition root via SetEnvelopeRegistry. P3b switches loadSchema to
// look up compiled schemas through this registry; until then it's a
// reference-keeper for orphan-aware lookups.
var (
	envelopeRegistryMu sync.RWMutex
	envelopeRegistry   *envelopes.Registry
)

// SetEnvelopeRegistry installs the shared registry used for envelope
// schema lookup. Called once at startup from cmd/nanite/main.go after
// envelopes.LoadCore. Passing nil unsets — useful for tests that want
// to fall back to the legacy embed.FS path.
func SetEnvelopeRegistry(r *envelopes.Registry) {
	envelopeRegistryMu.Lock()
	envelopeRegistry = r
	envelopeRegistryMu.Unlock()
}

// getEnvelopeRegistry returns the currently-installed registry (may be nil).
func getEnvelopeRegistry() *envelopes.Registry {
	envelopeRegistryMu.RLock()
	defer envelopeRegistryMu.RUnlock()
	return envelopeRegistry
}

// localePrinter is the printer the kind.* LocalizedString calls require.
// jsonschema/v6 keeps its own defaultPrinter unexported, so we maintain
// a parallel English printer here for our flatten walk. Cheap to allocate
// and reused across calls — see structuredErrorFromLeaf.
var localePrinter = message.NewPrinter(language.English)

// PassiveRenderableTypes is the v1 allow-list of envelope types the agent
// may emit through card_show. These are the cards that carry no
// decision-flow / runtime state — the agent decides "use a card with this
// data", the system decides where it lands. Decision-flow envelopes
// (approval-card, proposal-card, confirmation-card),
// runtime-emitted envelopes (chat-loop-terminated, elicitation-prompt,
// subagent-spawn-approval), and plugin-shipped envelopes (giphy-modal)
// intentionally have their own emission paths and are not addressable
// through show_card.
//
// Source of truth: SP-20260428-0001 / CW-20260428-0019 (Collab UI v1, A3).
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
//
// schemaRawCache stores the parsed schema document alongside the compiled
// validator so we can read custom annotation keywords (like
// `default_render_target`, A2 — CW-20260428-0008) that the compiler doesn't
// expose. Both caches are populated together by loadSchema so they stay
// consistent.
var (
	schemaCacheMu  sync.Mutex
	schemaCache    = map[string]*jsonschema.Schema{}
	schemaRawCache = map[string]map[string]any{}
)

// loadSchema returns a compiled schema for the given envelope type. The
// shared go-envelopes Registry is the only source post-Cap-5; bare names
// resolve to core types, and the nanite-legacy.<bare> alias covers the
// orphan schemas registered at startup. Returns an informative error
// when the registry is unset (composition-root setup mistake) or when
// the type isn't registered.
//
// The raw schema map (used by DefaultRenderTarget for the
// default_render_target annotation) is read from envelopes.EmbeddedFS()
// since lib registry TypeSpecs do not surface annotation bytes.
func loadSchema(envelopeType string) (*jsonschema.Schema, error) {
	schemaCacheMu.Lock()
	defer schemaCacheMu.Unlock()
	if cached, ok := schemaCache[envelopeType]; ok {
		return cached, nil
	}

	reg := getEnvelopeRegistry()
	if reg == nil {
		return nil, fmt.Errorf("envelope registry not configured (call SetEnvelopeRegistry at startup)")
	}
	compiled, ok := lookupRegistrySchema(reg, envelopeType)
	if !ok {
		return nil, fmt.Errorf("no schema registered for envelope type %q", envelopeType)
	}
	schemaCache[envelopeType] = compiled
	if doc, err := readEmbeddedSchemaDoc(envelopeType); err == nil {
		schemaRawCache[envelopeType] = doc
	}
	return compiled, nil
}

// lookupRegistrySchema fetches a compiled schema from the shared registry
// for envelopeType, attempting the bare name first and the
// nanite-legacy.<bare> alias second. Returns the compiled schema and a
// found-ok bool, treating a registered-but-schema-less spec as not-found.
func lookupRegistrySchema(reg *envelopes.Registry, envelopeType string) (*jsonschema.Schema, bool) {
	if spec, ok := reg.Lookup(envelopeType); ok && spec.DataSchema != nil {
		return spec.DataSchema, true
	}
	if spec, ok := reg.Lookup(LegacyTypeName(envelopeType)); ok && spec.DataSchema != nil {
		return spec.DataSchema, true
	}
	return nil, false
}

// readEmbeddedSchemaDoc reads the raw schema document from
// envelopes.EmbeddedFS() so DefaultRenderTarget can inspect annotation
// keywords (default_render_target). The lib's TypeSpec intentionally
// omits annotations — see the lib's known-limitations entry on
// "Schema annotation passthrough is one-way".
func readEmbeddedSchemaDoc(envelopeType string) (map[string]any, error) {
	libFS := envelopes.EmbeddedFS()
	raw, err := fs.ReadFile(libFS, "manifest/schemas/"+envelopeType+".schema.json")
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}


// DefaultRenderTarget returns the schema-declared default render-target for
// envelopeType, or "" if the schema does not declare one (or the type is
// unknown). The runtime stamps this onto Envelope.RenderTarget when the
// agent did not provide an explicit value (A2 — CW-20260428-0008).
//
// Defaults are limited to built-in panel IDs by convention so the schema
// path bypasses the trust gate that applies to explicit agent overrides.
// See docs/panels/envelope-render-target.md for the v1 default table.
func DefaultRenderTarget(envelopeType string) string {
	if _, err := loadSchema(envelopeType); err != nil {
		return ""
	}
	schemaCacheMu.Lock()
	doc := schemaRawCache[envelopeType]
	schemaCacheMu.Unlock()
	if doc == nil {
		return ""
	}
	v, _ := doc["default_render_target"].(string)
	return v
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

// StructuredError is the agent-facing shape for a single schema-validation
// failure. It is produced by FlattenSchemaError (and indirectly by
// ValidateAgainstSchema) so the tool_validate self-tool can hand the LLM
// a precise location, the underlying reason, and (when possible) a fix
// suggestion.
//
// Path is a JSON Pointer rooted at the validated value (RFC 6901). For
// validation rooted at the envelope's `data` field the path is relative to
// data — e.g. "/metrics/0/label" — which matches what the agent passed in.
//
// Reason is the jsonschema validator's localized message for the leaf
// failure, lower-cased and stripped of redundant quoting where it would
// just add noise.
//
// Suggestion is best-effort; an empty string means the validator did not
// recognise a pattern worth hinting at.
type StructuredError struct {
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion,omitempty"`
}

// FlattenSchemaError walks a *jsonschema.ValidationError tree, peels off
// any group/composite wrappers, and returns one StructuredError per leaf
// failure. err may be:
//   - nil → returns nil.
//   - a *jsonschema.ValidationError (or a wrapped one) → walked recursively.
//   - any other error → returned as a single-entry slice with Reason set
//     to err.Error(); Path defaults to "" and Suggestion is empty.
//
// The schemaDoc argument is the parsed schema document used to enrich
// AdditionalProperties / Required / Type errors with suggestions (e.g.
// the list of allowed property names). Pass nil to skip enrichment.
func FlattenSchemaError(err error, schemaDoc map[string]any) []StructuredError {
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []StructuredError{{Reason: err.Error()}}
	}
	var out []StructuredError
	collectLeaves(ve, schemaDoc, &out)
	if len(out) == 0 {
		// Defensive: walker found no leaves but a ValidationError was
		// passed in. Surface the raw message so the caller never gets
		// a silently-empty result.
		out = append(out, StructuredError{Reason: ve.Error()})
	}
	return out
}

// collectLeaves descends through Causes and emits one StructuredError per
// leaf (a node with no Causes). Group/AllOf/AnyOf/OneOf/Schema wrappers
// are flattened away because their messages are not actionable on their
// own — the leaf they wrap is what the agent needs to read.
func collectLeaves(ve *jsonschema.ValidationError, schemaDoc map[string]any, out *[]StructuredError) {
	if ve == nil {
		return
	}
	if len(ve.Causes) > 0 {
		for _, c := range ve.Causes {
			collectLeaves(c, schemaDoc, out)
		}
		return
	}
	*out = append(*out, structuredErrorFromLeaf(ve, schemaDoc))
}

// structuredErrorFromLeaf converts a leaf *jsonschema.ValidationError into
// a StructuredError, picking suggestions off recognised ErrorKinds.
func structuredErrorFromLeaf(ve *jsonschema.ValidationError, schemaDoc map[string]any) StructuredError {
	se := StructuredError{
		Path:   jsonPointer(ve.InstanceLocation),
		Reason: ve.ErrorKind.LocalizedString(localePrinter),
	}
	switch k := ve.ErrorKind.(type) {
	case *kind.Required:
		// "missing properties 'x', 'y'" — already informative; add a
		// suggestion that lists the missing names compactly so the LLM
		// can pluck them.
		if len(k.Missing) > 0 {
			if len(k.Missing) == 1 {
				se.Suggestion = "add required field " + quote(k.Missing[0])
			} else {
				se.Suggestion = "add required fields " + joinQuoted(k.Missing)
			}
		}
	case *kind.AdditionalProperties:
		// AdditionalProperties errors fire on the parent object; the
		// rejected key sits on the schema's allowed list. Walk schemaDoc
		// to the parent's `properties` and surface the allowed key set.
		allowed := allowedPropertyNames(schemaDoc, ve.InstanceLocation)
		if len(allowed) > 0 {
			se.Suggestion = "allowed: " + strings.Join(allowed, ", ")
		}
	case *kind.Type:
		// "got X, want Y" — when we expected an array but got an object,
		// the most common LLM mistake is to wrap a single item in {} where
		// the schema wants [].
		if len(k.Want) == 1 {
			want := k.Want[0]
			if want == "array" && k.Got == "object" {
				se.Suggestion = "wrap in `[...]`"
			} else if want == "array" {
				se.Suggestion = "wrap the value in an array: `[...]`"
			} else if want == "string" && (k.Got == "number" || k.Got == "integer" || k.Got == "boolean") {
				se.Suggestion = "stringify the value (use quotes)"
			}
		}
	case *kind.Enum:
		// Enums advertise their allowed values via the schema doc; the
		// kind's Want carries them directly so we can surface the set.
		if len(k.Want) > 0 {
			vals := make([]string, 0, len(k.Want))
			for _, v := range k.Want {
				if s, ok := v.(string); ok {
					vals = append(vals, s)
				}
			}
			if len(vals) > 0 {
				se.Suggestion = "allowed values: " + strings.Join(vals, ", ")
			}
		}
	}
	return se
}

// jsonPointer renders an InstanceLocation slice as an RFC 6901 JSON
// pointer rooted at the validated value. An empty path becomes "" (root)
// rather than "/" — the validator passes "" to mean "the value itself".
func jsonPointer(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		// RFC 6901 escaping: "~" → "~0", "/" → "~1".
		p = strings.ReplaceAll(p, "~", "~0")
		p = strings.ReplaceAll(p, "/", "~1")
		b.WriteString(p)
	}
	return b.String()
}

// allowedPropertyNames returns the list of property names the schema
// declares at the location pointed to by instanceLoc. Used to render an
// "allowed: a, b, c" suggestion when an additionalProperties error fires.
//
// instanceLoc is the InstanceLocation slice from the *parent* of the
// rejected property (jsonschema reports the AdditionalProperties error
// against the object, not the offending key). schemaDoc must be the
// parsed schema for the same root.
//
// Returns nil if traversal can't resolve a sub-schema for the location
// (e.g. the schema uses $ref or oneOf in a way the simple walk can't
// follow). Conservative on purpose: a missing suggestion is fine, but
// hallucinated allowed names would be worse.
func allowedPropertyNames(schemaDoc map[string]any, instanceLoc []string) []string {
	if schemaDoc == nil {
		return nil
	}
	cur := schemaDoc
	for _, seg := range instanceLoc {
		props, _ := cur["properties"].(map[string]any)
		if props == nil {
			return nil
		}
		next, _ := props[seg].(map[string]any)
		if next == nil {
			// The instance walked into an array or an unknown key —
			// can't resolve allowed names for the parent.
			//
			// Fall back to checking `items` for arrays.
			if items, ok := cur["items"].(map[string]any); ok {
				cur = items
				continue
			}
			return nil
		}
		cur = next
	}
	props, _ := cur["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	// Sort so the suggestion is stable across calls — the schema map
	// iteration order is non-deterministic and the LLM-facing message
	// should not jitter between calls.
	sortStrings(names)
	return names
}

// quote wraps s in single quotes so suggestion strings read naturally
// regardless of whether s is a Go string or a property name.
func quote(s string) string { return "'" + s + "'" }

// joinQuoted returns "'a', 'b', 'c'" for a slice of names — used in
// multi-missing-required suggestions.
func joinQuoted(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, quote(n))
	}
	return strings.Join(quoted, ", ")
}

// sortStrings is a tiny in-place insertion sort. Avoids importing "sort"
// for the four or five calls in this package (and keeps the dep graph
// trim — this package is imported by both backend and validation tests).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// ValidateAgainstSchema validates `value` against an arbitrary JSON
// schema (provided as a parsed map[string]any) and returns the structured
// error list. It compiles the schema on demand — callers that validate
// the same schema repeatedly should cache the *jsonschema.Schema
// themselves; this helper is sized for one-shot tool-input validation
// where the schema comes from the tool registry rather than a fixed file.
//
// Returns (nil, nil) on success. Returns ([]StructuredError{...}, nil) on
// validation failure. Returns (nil, error) only if the schema itself is
// malformed and won't compile — that's a programmer error, not an
// agent-fixable input.
//
// The synthetic schema URI is unique per call to avoid colliding with
// other schemas the package has already compiled (the "mem://" scheme
// keeps file URLs out of error messages — see schemaResourceURI).
//
// Caveat: Go schema literals built with map[string]any commonly carry
// concrete-typed slices like []string for `required`/`enum` that won't
// satisfy jsonschema/v6's metaschema (it expects []any). We round-trip
// through json.Marshal/Unmarshal at the boundary to coerce those into
// the canonical interface-typed shape, mirroring how the loadSchema
// path consumes embedded JSON files. The cost is one allocation per
// call; tool_validate is rare enough on the hot path that this is
// fine.
func ValidateAgainstSchema(schemaDoc map[string]any, value any) ([]StructuredError, error) {
	if schemaDoc == nil {
		return nil, fmt.Errorf("schema is nil")
	}
	normalised, err := normaliseSchemaDoc(schemaDoc)
	if err != nil {
		return nil, fmt.Errorf("normalise schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	uri := "mem://nanite/tool-input/inline.schema.json"
	if err := compiler.AddResource(uri, any(normalised)); err != nil {
		return nil, fmt.Errorf("register schema: %w", err)
	}
	compiled, err := compiler.Compile(uri)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	if err := compiled.Validate(value); err != nil {
		return FlattenSchemaError(err, normalised), nil
	}
	return nil, nil
}

// normaliseSchemaDoc round-trips a schema literal through JSON to coerce
// concrete-typed slices ([]string) and concrete-typed map values into
// the []any / map[string]any shape jsonschema/v6's metaschema expects.
// The Go-literal tool schemas in self_tools.go use []string for enums
// and required lists; without this step the compiler rejects them at
// metaschema validation.
func normaliseSchemaDoc(schemaDoc map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(schemaDoc)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateEnvelopeData is a structured-error wrapper around ValidateData.
// It returns the same []StructuredError shape as ValidateAgainstSchema so
// callers can treat envelope and tool-input validation uniformly.
//
// Returns (nil, nil) when the envelope type is known and `data` matches
// the schema. Returns ([]StructuredError{...}, nil) when it doesn't.
// Returns (nil, error) when envelopeType is unregistered — that's an
// orchestration bug, not a fixable input error.
func ValidateEnvelopeData(envelopeType string, data any) ([]StructuredError, error) {
	if _, err := loadSchema(envelopeType); err != nil {
		return nil, err
	}
	schemaCacheMu.Lock()
	doc := schemaRawCache[envelopeType]
	schemaCacheMu.Unlock()
	if err := ValidateData(envelopeType, data); err != nil {
		return FlattenSchemaError(err, doc), nil
	}
	return nil, nil
}
