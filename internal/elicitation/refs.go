package elicitation

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
)

// MaxSchemaBytes bounds a schema the model writes itself (a subtask's
// return_spec, an ask_user question).
const MaxSchemaBytes = 16 << 10

// ResolveLimited is Resolve for a schema the model wrote. Such a schema is
// held to a strict subset, because jsonschema-go can recurse without end on
// a schema that refers back to itself, and that overflows the stack, which
// no recover catches:
//   - at most MaxSchemaBytes;
//   - no $id, $anchor, $dynamicAnchor, $dynamicRef or $recursiveRef;
//   - a $ref only of the form #/$defs/<name> or #/definitions/<name>
//     (percent- and pointer-decoded), naming a definition that exists;
//   - following $ref and the keywords that apply to the same value (allOf,
//     anyOf, oneOf, not, if, then, else, dependentSchemas, dependencies)
//     never leads back to a schema already on the path. Recursion through
//     properties, items and the like is fine: each step is into a smaller
//     value.
//
// Tool input schemas from hosts and MCP servers go through Resolve alone,
// which holds every schema to the limits in guard.go (no in-place cycle,
// bounded in-place chains and paths, size and nesting) without this subset.
func ResolveLimited(raw json.RawMessage) (*jsonschema.Resolved, error) {
	if len(raw) > MaxSchemaBytes {
		return nil, fmt.Errorf("schema is %d bytes; the limit is %d", len(raw), MaxSchemaBytes)
	}
	// jsonschema-go reads keywords case-insensitively (encoding/json into its
	// struct, the last of duplicate keys winning), so check exact spellings
	// first and then the library's own reading of the schema.
	if err := checkSpelling(raw); err != nil {
		return nil, err
	}
	canonical, err := canonicalize(raw)
	if err != nil {
		return nil, err
	}
	if err := checkStrict(canonical); err != nil {
		return nil, err
	}
	return Resolve(raw)
}

// canonicalize is the schema as jsonschema-go reads it, written back with
// its canonical keyword spellings.
func canonicalize(raw json.RawMessage) (json.RawMessage, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return json.Marshal(&schema)
}

// keywords are the JSON Schema keywords jsonschema-go reads, in their only
// accepted spelling.
var keywords = []string{
	"$schema", "$id", "$ref", "$comment", "$defs", "definitions", "$anchor", "$dynamicAnchor", "$dynamicRef", "$recursiveRef", "$recursiveAnchor", "$vocabulary",
	"type", "enum", "const", "multipleOf", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
	"minLength", "maxLength", "pattern", "prefixItems", "items", "additionalItems", "minItems", "maxItems", "uniqueItems",
	"contains", "minContains", "maxContains", "unevaluatedItems", "minProperties", "maxProperties", "required",
	"dependentRequired", "dependencies", "properties", "patternProperties", "additionalProperties", "propertyNames",
	"unevaluatedProperties", "allOf", "anyOf", "oneOf", "not", "if", "then", "else", "dependentSchemas",
	"contentEncoding", "contentMediaType", "contentSchema", "format", "title", "description", "default",
	"deprecated", "readOnly", "writeOnly", "examples",
}

// nameMaps are keywords whose object values are keyed by names (property
// or definition names), not keywords.
var nameMaps = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies", "dependentRequired"}

// checkSpelling refuses, at every level, a key that matches a keyword
// ignoring case but is not spelled exactly like it. Names under properties,
// $defs and the like are names, not keywords, and may be spelled freely;
// their values are still checked.
func checkSpelling(raw json.RawMessage) error {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	return spelling(doc, false)
}

func spelling(value any, names bool) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if !names {
				for _, keyword := range keywords {
					if key != keyword && strings.EqualFold(key, keyword) {
						return fmt.Errorf("schema key %q must be spelled %q", key, keyword)
					}
				}
			}
			isNameMap := false
			if !names {
				for _, k := range nameMaps {
					if strings.EqualFold(key, k) {
						isNameMap = true
					}
				}
			}
			if err := spelling(child, isNameMap); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := spelling(child, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// Keywords whose values are schemas that apply to the same value (in place)
// or to a part of it (descending), as jsonschema-go reads them.
var (
	inPlaceOne  = []string{"not", "if", "then", "else"}
	inPlaceList = []string{"allOf", "anyOf", "oneOf"}
	inPlaceMap  = []string{"dependentSchemas", "dependencies"}
	descendOne  = []string{"additionalItems", "contains", "unevaluatedItems", "additionalProperties", "propertyNames", "unevaluatedProperties", "contentSchema", "items"}
	descendList = []string{"prefixItems", "items"}
	descendMap  = []string{"properties", "patternProperties"}
	forbidden   = []string{"$id", "$anchor", "$dynamicAnchor", "$dynamicRef", "$recursiveRef"}
)

// strictCheck walks a model-written schema keyword by keyword.
type strictCheck struct {
	defs map[string]any // "$defs/<name>" or "definitions/<name>" -> schema
	// edges maps a node ("" for the root, else a defs key) to the
	// definitions it reaches in place.
	edges map[string][]string
}

func checkStrict(raw json.RawMessage) error {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	c := strictCheck{defs: map[string]any{}, edges: map[string][]string{}}
	if top, ok := root.(map[string]any); ok {
		// The root's definitions are graph nodes of their own; walk the root
		// without them.
		rest := make(map[string]any, len(top))
		for key, value := range top {
			rest[key] = value
		}
		for _, kind := range []string{"$defs", "definitions"} {
			if defs, ok := top[kind].(map[string]any); ok {
				for name, def := range defs {
					c.defs[kind+"/"+name] = def
				}
			}
			delete(rest, kind)
		}
		root = rest
	}
	if err := c.walk(root, "", true); err != nil {
		return err
	}
	for key, def := range c.defs {
		if err := c.walk(def, key, true); err != nil {
			return err
		}
	}
	return c.acyclic()
}

// walk checks one schema. node is the graph node it belongs to; inPlace is
// whether it applies to the same value as node, so that a $ref here is an
// edge of node's graph.
func (c *strictCheck) walk(value any, node string, inPlace bool) error {
	schema, ok := value.(map[string]any)
	if !ok {
		return nil // true, false, or a malformed value Resolve reports.
	}
	for _, key := range forbidden {
		if _, ok := schema[key]; ok {
			return fmt.Errorf("schema keyword %s is not supported in a return spec or question", key)
		}
	}
	if ref, ok := schema["$ref"]; ok {
		target, err := c.target(ref)
		if err != nil {
			return err
		}
		if inPlace {
			c.edges[node] = append(c.edges[node], target)
		}
	}
	for _, key := range inPlaceOne {
		if err := c.walk(schema[key], node, inPlace); err != nil {
			return err
		}
	}
	for _, key := range inPlaceList {
		list, _ := schema[key].([]any)
		for _, item := range list {
			if err := c.walk(item, node, inPlace); err != nil {
				return err
			}
		}
	}
	for _, key := range inPlaceMap {
		entries, _ := schema[key].(map[string]any)
		for _, item := range entries {
			if err := c.walk(item, node, inPlace); err != nil {
				return err
			}
		}
	}
	for _, key := range descendOne {
		if err := c.walk(schema[key], node, false); err != nil {
			return err
		}
	}
	for _, key := range descendList {
		list, _ := schema[key].([]any)
		for _, item := range list {
			if err := c.walk(item, node, false); err != nil {
				return err
			}
		}
	}
	for _, key := range descendMap {
		entries, _ := schema[key].(map[string]any)
		for _, item := range entries {
			if err := c.walk(item, node, false); err != nil {
				return err
			}
		}
	}
	// Definitions below the root are never a $ref target (only the root's
	// are), so they add no edges; check their contents all the same.
	for _, kind := range []string{"$defs", "definitions"} {
		entries, _ := schema[kind].(map[string]any)
		for _, item := range entries {
			if err := c.walk(item, node, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// target resolves a $ref to the definition it names.
func (c *strictCheck) target(ref any) (string, error) {
	text, ok := ref.(string)
	if !ok {
		return "", fmt.Errorf("schema $ref must be a string")
	}
	for _, kind := range []string{"$defs", "definitions"} {
		prefix := "#/" + kind + "/"
		if !strings.HasPrefix(text, prefix) {
			continue
		}
		name, err := url.PathUnescape(strings.TrimPrefix(text, prefix))
		if err != nil || name == "" || strings.Contains(name, "/") {
			break
		}
		name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
		key := kind + "/" + name
		if _, ok := c.defs[key]; !ok {
			return "", fmt.Errorf("schema $ref %q names no definition", text)
		}
		return key, nil
	}
	return "", fmt.Errorf("schema $ref %q: only #/$defs/<name> or #/definitions/<name> is supported in a return spec or question", text)
}

// acyclic refuses a definition reachable from itself in place.
func (c *strictCheck) acyclic() error {
	const (
		onPath = 1
		done   = 2
	)
	state := map[string]int{}
	var visit func(string) error
	visit = func(node string) error {
		switch state[node] {
		case onPath:
			return fmt.Errorf("schema definition %s refers to itself without moving into a property or item; validation would never end", node)
		case done:
			return nil
		}
		state[node] = onPath
		for _, next := range c.edges[node] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[node] = done
		return nil
	}
	for node := range c.edges {
		if err := visit(node); err != nil {
			return err
		}
	}
	return nil
}
