// Package clientcontext validates the embedded-client view contract.
// Descriptors are untrusted observations, never identities, grants or tools.
package clientcontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Proposed host limits apply before effects. Oversize input is refused, not
// truncated. MaxBytes also bounds the normalized JSON sent to the model.
const (
	Version             = 1
	MaxBytes            = 32 * 1024
	MaxDepth            = 16
	MaxNodes            = 2048
	MaxRouteBytes       = 256
	MaxFilters          = 16
	MaxFilterNameBytes  = 64
	MaxFilterValues     = 16
	MaxFilterValueBytes = 256
	MaxSearchBytes      = 1024
	MaxSelectedIDs      = 64
	MaxIDBytes          = 128
	MaxVisibleRows      = 32
	MaxSummaryBytes     = 512
	MaxCommands         = 16
	MaxCommandNameBytes = 128
	MaxSchemaBytes      = 2048
)

type Descriptor struct {
	Version int  `json:"version"`
	View    View `json:"view"`
}

type View struct {
	Route             string    `json:"route"`
	ActiveFilters     []Filter  `json:"active_filters,omitempty"`
	Search            string    `json:"search,omitempty"`
	SelectedIDs       []string  `json:"selected_ids,omitempty"`
	VisibleRows       []Row     `json:"visible_rows,omitempty"`
	AvailableCommands []Command `json:"available_commands,omitempty"`
}

type Filter struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Row carries only the sender's opt-in display summary, never a raw record.
type Row struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// Command is descriptive bounded JSON only. Decode neither resolves $ref
// in InputSchema nor installs, authorizes or executes a declared command.
type Command struct {
	Name        string          `json:"name"`
	Scope       string          `json:"scope"` // ephemeral or url-backed
	InputSchema json.RawMessage `json:"input_schema"`
}

// Snapshot owns immutable normalized bytes detached from the request buffer.
// It is intentionally not a serializable transcript/metadata record. JSON is
// an explicit projection for the transient provider input, not a log method.
type Snapshot struct{ body string }

func (s Snapshot) JSON() string   { return s.body }
func (s Snapshot) Empty() bool    { return s.body == "" }
func (Snapshot) String() string   { return "client_context v1 (untrusted, ephemeral)" }
func (Snapshot) GoString() string { return "clientcontext.Snapshot{redacted}" }

// MarshalJSON refuses accidental metadata, transcript or replay persistence.
// The explicit JSON projection is reserved for transient provider input.
func (Snapshot) MarshalJSON() ([]byte, error) {
	return nil, errors.New("ephemeral client_context snapshot is not a persistence or replay record")
}

// ValidationError reports only schema paths and fixed reasons, not view data.
type ValidationError struct{ Path, Reason string }

func (e *ValidationError) Error() string {
	return "invalid client_context " + e.Path + ": " + e.Reason
}

func invalid(path, reason string) error { return &ValidationError{path, reason} }

func Decode(raw []byte) (Snapshot, error) {
	if len(raw) == 0 || len(raw) > MaxBytes {
		return Snapshot{}, invalid("", "JSON byte limit exceeded or missing input")
	}
	if !utf8.Valid(raw) || !json.Valid(raw) || !validUnicodeEscapes(raw) {
		return Snapshot{}, invalid("", "valid UTF-8 JSON with paired Unicode escapes required")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	if err := checkJSON(d, 0, &nodes); err != nil {
		return Snapshot{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return Snapshot{}, invalid("", "one JSON value required")
	}

	var shape any
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&shape); err != nil {
		return Snapshot{}, invalid("", "invalid JSON shape")
	}
	if !noNullOutsideSchema(shape) {
		return Snapshot{}, invalid("", "null fields are unsupported outside input_schema")
	}
	if !exactFields(shape, "root") {
		return Snapshot{}, invalid("", "unknown field or invalid object shape")
	}

	var value Descriptor
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return Snapshot{}, invalid("", "unknown field or invalid field type")
	}
	if err := validate(value); err != nil {
		return Snapshot{}, err
	}
	body, err := json.Marshal(value)
	if err != nil || len(body) > MaxBytes {
		return Snapshot{}, invalid("", "normalized JSON byte limit exceeded")
	}
	return Snapshot{body: string(body)}, nil
}

// encoding/json struct decoding accepts case aliases. The wire is exact and
// closed at every DTO object; descriptive schema objects remain opaque data.
func exactFields(value any, kind string) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	allowed := map[string]string{}
	switch kind {
	case "root":
		allowed = map[string]string{"version": "", "view": "view"}
	case "view":
		allowed = map[string]string{"route": "", "active_filters": "filters", "search": "", "selected_ids": "", "visible_rows": "rows", "available_commands": "commands"}
	case "filter":
		allowed = map[string]string{"name": "", "values": ""}
	case "row":
		allowed = map[string]string{"id": "", "summary": ""}
	case "command":
		allowed = map[string]string{"name": "", "scope": "", "input_schema": ""}
	}
	for key, child := range object {
		nested, present := allowed[key]
		if !present {
			return false
		}
		if nested == "view" && !exactFields(child, nested) {
			return false
		}
		if nested == "filters" || nested == "rows" || nested == "commands" {
			items, isArray := child.([]any)
			if !isArray {
				return false
			}
			for _, item := range items {
				if !exactFields(item, strings.TrimSuffix(nested, "s")) {
					return false
				}
			}
		}
	}
	return true
}

func checkJSON(d *json.Decoder, depth int, nodes *int) error {
	*nodes++
	if depth > MaxDepth || *nodes > MaxNodes {
		return invalid("", "JSON depth or node limit exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return invalid("", "invalid JSON")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	keys := map[string]bool{}
	for d.More() {
		if delim == '{' {
			keyToken, keyErr := d.Token()
			key, isString := keyToken.(string)
			if keyErr != nil || !isString || keys[key] {
				return invalid("", "duplicate or invalid JSON object key")
			}
			keys[key] = true
		}
		if err := checkJSON(d, depth+1, nodes); err != nil {
			return err
		}
	}
	if _, err := d.Token(); err != nil {
		return invalid("", "invalid JSON closure")
	}
	return nil
}

func noNullOutsideSchema(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case []any:
		for _, child := range v {
			if !noNullOutsideSchema(child) {
				return false
			}
		}
	case map[string]any:
		for key, child := range v {
			if key != "input_schema" && !noNullOutsideSchema(child) {
				return false
			}
		}
	}
	return true
}

// encoding/json replaces lone surrogate escapes; refuse them before decoding
// so an invalid Unicode identifier cannot silently change during admission.
func validUnicodeEscapes(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		code, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if code >= 0xDC00 && code <= 0xDFFF {
			return false
		}
		if code >= 0xD800 && code <= 0xDBFF {
			if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
				return false
			}
			low, lowErr := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if lowErr != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 11
		} else {
			i += 5
		}
	}
	return true
}

func text(path, value string, limit int, required bool) error {
	if !utf8.ValidString(value) || len(value) > limit || (required && value == "") {
		return invalid(path, "missing text, invalid UTF-8 or byte limit exceeded")
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return invalid(path, "control character unsupported")
		}
	}
	return nil
}

func identifier(path, value string, limit int) error {
	if err := text(path, value, limit, true); err != nil {
		return err
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return invalid(path, "identifier whitespace unsupported")
		}
	}
	return nil
}

func validate(value Descriptor) error {
	if value.Version != Version {
		return invalid("version", "supported version is 1")
	}
	v := value.View
	if err := text("view.route", v.Route, MaxRouteBytes, true); err != nil {
		return err
	}
	route, err := url.Parse(v.Route)
	if err != nil || !strings.HasPrefix(v.Route, "/") || strings.HasPrefix(v.Route, "//") || strings.ContainsAny(v.Route, "\\?#") || strings.HasPrefix(route.Path, "//") || strings.Contains(route.Path, "\\") || route.Scheme != "" || route.Host != "" {
		return invalid("view.route", "origin-relative pathname required, without query or fragment")
	}
	if err = identifier("view.route", route.Path, MaxRouteBytes); err != nil {
		return err
	}
	if len(v.ActiveFilters) > MaxFilters || len(v.SelectedIDs) > MaxSelectedIDs || len(v.VisibleRows) > MaxVisibleRows || len(v.AvailableCommands) > MaxCommands {
		return invalid("view", "collection count limit exceeded")
	}
	if err = text("view.search", v.Search, MaxSearchBytes, false); err != nil {
		return err
	}
	filterNames := map[string]bool{}
	for i, filter := range v.ActiveFilters {
		path := fmt.Sprintf("view.active_filters[%d]", i)
		if err = identifier(path+".name", filter.Name, MaxFilterNameBytes); err != nil {
			return err
		}
		if filterNames[filter.Name] || len(filter.Values) == 0 || len(filter.Values) > MaxFilterValues {
			return invalid(path, "duplicate name or unsupported value count")
		}
		filterNames[filter.Name] = true
		for _, item := range filter.Values {
			if err = text(path+".values", item, MaxFilterValueBytes, false); err != nil {
				return err
			}
		}
	}
	ids := map[string]bool{}
	for _, id := range v.SelectedIDs {
		if err = identifier("view.selected_ids", id, MaxIDBytes); err != nil {
			return err
		}
		if ids[id] {
			return invalid("view.selected_ids", "duplicate identifier")
		}
		ids[id] = true
	}
	ids = map[string]bool{}
	for i, row := range v.VisibleRows {
		path := fmt.Sprintf("view.visible_rows[%d]", i)
		if err = identifier(path+".id", row.ID, MaxIDBytes); err != nil {
			return err
		}
		if ids[row.ID] {
			return invalid(path, "duplicate identifier")
		}
		ids[row.ID] = true
		if err = text(path+".summary", row.Summary, MaxSummaryBytes, false); err != nil {
			return err
		}
	}
	names := map[string]bool{}
	for i, command := range v.AvailableCommands {
		path := fmt.Sprintf("view.available_commands[%d]", i)
		if err = identifier(path+".name", command.Name, MaxCommandNameBytes); err != nil {
			return err
		}
		if names[command.Name] {
			return invalid(path, "duplicate command name")
		}
		names[command.Name] = true
		if command.Scope != "ephemeral" && command.Scope != "url-backed" {
			return invalid(path+".scope", "ephemeral or url-backed required")
		}
		schema, schemaErr := json.Marshal(command.InputSchema)
		if schemaErr != nil || len(schema) > MaxSchemaBytes || len(command.InputSchema) == 0 || bytes.TrimSpace(command.InputSchema)[0] != '{' {
			return invalid(path+".input_schema", "bounded JSON object required")
		}
	}
	return nil
}
