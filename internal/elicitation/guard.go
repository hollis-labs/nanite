package elicitation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// jsonschema-go v0.4.3 validates by plain recursion and explores every
// alternative of anyOf, allOf and the like. Some schemas therefore never
// finish, or overflow the stack (a fatal runtime error that no recover
// catches, so it ends the whole process), or exhaust memory. Every schema
// Nanite resolves, from the model, a host or an MCP server, is held to these
// limits, and every value is checked before it is validated.
const (
	// MaxToolSchemaBytes bounds a tool input schema from a host or MCP
	// server. A schema the model writes is held to MaxSchemaBytes.
	MaxToolSchemaBytes = 256 << 10
	// MaxSchemaNesting bounds how deeply a schema's JSON nests.
	MaxSchemaNesting = 256
	// MaxInPlaceChain bounds the longest run of keywords that apply to the
	// same value ($ref, allOf, anyOf, oneOf, not, if/then/else, ...).
	MaxInPlaceChain = 32
	// MaxInPlacePaths bounds how many such runs start at one schema: the
	// work Validate does for one location in a value.
	MaxInPlacePaths = 1000
	// MaxValueDepth bounds how deeply a value (tool input, a returned result,
	// an answer) nests before it is validated.
	MaxValueDepth = 64
	// MaxValidationError bounds the validation error text passed on.
	MaxValidationError = 4 << 10
)

// ErrUnsafeSchema marks a schema Nanite will not validate against: one that
// refers back to itself in place, is too large, nests too deeply, or makes
// validation do too much work.
var ErrUnsafeSchema = errors.New("unsafe schema")

// ErrValueTooDeep marks a value nested more than MaxValueDepth levels.
var ErrValueTooDeep = errors.New("value nests too deeply")

// Validate checks value against a resolved schema. A value nested more than
// MaxValueDepth levels is refused before validation, and the error text is
// cut to MaxValidationError bytes.
func Validate(schema *jsonschema.Resolved, value any) error {
	if depth(value, 0) > MaxValueDepth {
		return fmt.Errorf("%w: more than %d levels", ErrValueTooDeep, MaxValueDepth)
	}
	err := schema.Validate(value)
	if err == nil {
		return nil
	}
	if text := err.Error(); len(text) > MaxValidationError {
		return errors.New(cut(text, MaxValidationError) + " … (truncated)")
	}
	return err
}

// depth is how deeply a decoded JSON value nests, stopping once past the
// limit so a pathological value costs no more than the limit.
func depth(value any, level int) int {
	if level > MaxValueDepth {
		return level
	}
	deepest := level
	switch v := value.(type) {
	case map[string]any:
		for _, c := range v {
			if d := depth(c, level+1); d > deepest {
				deepest = d
				if d > MaxValueDepth {
					return d
				}
			}
		}
	case []any:
		for _, c := range v {
			if d := depth(c, level+1); d > deepest {
				deepest = d
				if d > MaxValueDepth {
					return d
				}
			}
		}
	}
	return deepest
}

func cut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && limit < len(text) && text[limit]&0xC0 == 0x80 {
		limit--
	}
	return text[:limit]
}

// jsonNesting is the deepest object/array nesting in raw JSON, read without
// decoding, so a hostile document is measured before anything recurses on it.
func jsonNesting(raw []byte) int {
	level, deepest, inString, escaped := 0, 0, false, false
	for _, b := range raw {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
		case b == '"':
			inString = true
		case b == '{' || b == '[':
			level++
			deepest = max(deepest, level)
		case b == '}' || b == ']':
			level--
		}
	}
	return deepest
}

// resolveCache keeps resolved schemas by content, so a tool's schema is
// resolved once rather than on every turn. A Resolved is read-only and safe
// to share between goroutines.
var resolveCache = struct {
	sync.Mutex
	entries map[[sha256.Size]byte]resolvedEntry
}{entries: map[[sha256.Size]byte]resolvedEntry{}}

type resolvedEntry struct {
	schema *jsonschema.Resolved
	err    error
}

const resolveCacheEntries = 1024

func cachedResolve(raw []byte, resolve func() (*jsonschema.Resolved, error)) (*jsonschema.Resolved, error) {
	key := sha256.Sum256(raw)
	resolveCache.Lock()
	entry, ok := resolveCache.entries[key]
	resolveCache.Unlock()
	if ok {
		return entry.schema, entry.err
	}
	schema, err := resolve()
	resolveCache.Lock()
	if len(resolveCache.entries) >= resolveCacheEntries {
		clear(resolveCache.entries)
	}
	resolveCache.entries[key] = resolvedEntry{schema, err}
	resolveCache.Unlock()
	return schema, err
}

// checkResolved refuses a resolved schema that validation could not finish
// on: an in-place cycle, a run of in-place keywords longer than
// MaxInPlaceChain, or more than MaxInPlacePaths such runs from one schema.
//
// "In place" means the keyword applies its subschemas to the same value:
// $ref, $dynamicRef, allOf, anyOf, oneOf, not, if, then, else,
// dependentSchemas and draft-07 schema dependencies. Recursion through
// properties, items and the like is fine: each step is into a smaller part
// of a finite value (whose depth Validate bounds).
//
// The $ref and $dynamicRef targets are the library's own resolution (its $id
// bases, anchors, percent- and pointer-decoding), read from the Resolved, so
// the check agrees with what Validate will follow. A dynamic reference that
// resolves at validation time is taken to reach every schema with a matching
// $dynamicAnchor, which can only over-report.
func checkResolved(rs *jsonschema.Resolved) error {
	root := rs.Schema()
	if root == nil {
		return nil
	}
	order, paths := index(root)
	nodes := make(map[uintptr]*jsonschema.Schema, len(order))
	anchors := map[string][]*jsonschema.Schema{}
	for _, s := range order {
		nodes[reflect.ValueOf(s).Pointer()] = s
		if s.DynamicAnchor != "" {
			anchors[s.DynamicAnchor] = append(anchors[s.DynamicAnchor], s)
		}
	}
	refs, err := resolvedTargets(rs, nodes)
	if err != nil {
		for _, s := range order {
			if s.Ref != "" || s.DynamicRef != "" {
				return fmt.Errorf("%w: cannot check this schema (library layout changed): %w", ErrUnsafeSchema, err)
			}
		}
		refs = nil
	}

	edges := func(s *jsonschema.Schema) []*jsonschema.Schema {
		var out []*jsonschema.Schema
		if t, ok := refs[s]; ok {
			out = append(out, t.targets...)
			if t.dynamicAnchor != "" {
				out = append(out, anchors[t.dynamicAnchor]...)
			}
		}
		out = append(out, s.AllOf...)
		out = append(out, s.AnyOf...)
		out = append(out, s.OneOf...)
		for _, c := range []*jsonschema.Schema{s.Not, s.If, s.Then, s.Else} {
			if c != nil {
				out = append(out, c)
			}
		}
		for _, k := range slices.Sorted(maps.Keys(s.DependentSchemas)) {
			out = append(out, s.DependentSchemas[k])
		}
		for _, k := range slices.Sorted(maps.Keys(s.DependencySchemas)) {
			out = append(out, s.DependencySchemas[k])
		}
		return out
	}

	type measure struct{ chain, paths int }
	memo := map[*jsonschema.Schema]measure{}
	onPath := map[*jsonschema.Schema]bool{}
	var stack []*jsonschema.Schema
	trail := func(end *jsonschema.Schema) string {
		var parts []string
		for _, s := range stack {
			parts = append(parts, paths[s])
		}
		return strings.Join(append(parts, paths[end]), " -> ")
	}
	var visit func(*jsonschema.Schema) (measure, error)
	visit = func(s *jsonschema.Schema) (measure, error) {
		if m, ok := memo[s]; ok {
			return m, nil
		}
		if onPath[s] {
			start := slices.Index(stack, s)
			loop := stack[start:]
			parts := make([]string, 0, len(loop)+1)
			for _, n := range loop {
				parts = append(parts, paths[n])
			}
			return measure{}, fmt.Errorf("%w: it refers back to itself in place: %s", ErrUnsafeSchema, strings.Join(append(parts, paths[s]), " -> "))
		}
		if len(stack) >= MaxInPlaceChain {
			return measure{}, fmt.Errorf("%w: more than %d keywords apply to the same value in a row: %s", ErrUnsafeSchema, MaxInPlaceChain, trail(s))
		}
		onPath[s] = true
		stack = append(stack, s)
		m := measure{chain: 1, paths: 1}
		for _, next := range edges(s) {
			c, err := visit(next)
			if err != nil {
				return measure{}, err
			}
			m.chain = max(m.chain, c.chain+1)
			m.paths = min(m.paths+c.paths, MaxInPlacePaths+1)
		}
		stack = stack[:len(stack)-1]
		delete(onPath, s)
		if m.chain > MaxInPlaceChain {
			return measure{}, fmt.Errorf("%w: more than %d keywords apply to the same value in a row from %s", ErrUnsafeSchema, MaxInPlaceChain, paths[s])
		}
		if m.paths > MaxInPlacePaths {
			return measure{}, fmt.Errorf("%w: more than %d ways to apply it to one value from %s", ErrUnsafeSchema, MaxInPlacePaths, paths[s])
		}
		memo[s] = m
		return m, nil
	}
	// Every subschema can be reached by validation (through properties and
	// the like), so every one is checked.
	for _, s := range order {
		if _, err := visit(s); err != nil {
			return err
		}
	}
	return nil
}

// index lists every schema under root depth first, in a fixed order (fields
// in declaration order, map keys sorted), with its JSON path for error
// messages. It walks the exported schema-valued fields, as the library itself
// does.
func index(root *jsonschema.Schema) ([]*jsonschema.Schema, map[*jsonschema.Schema]string) {
	var order []*jsonschema.Schema
	paths := map[*jsonschema.Schema]string{}
	var walk func(*jsonschema.Schema, string)
	walk = func(s *jsonschema.Schema, path string) {
		if s == nil {
			return
		}
		if _, seen := paths[s]; seen {
			return
		}
		paths[s] = path
		order = append(order, s)
		v := reflect.ValueOf(s).Elem()
		t := v.Type()
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, fv := jsonName(f), v.Field(i)
			switch f.Type {
			case schemaType:
				walk(fv.Interface().(*jsonschema.Schema), path+"/"+name)
			case schemaSliceType:
				for j, c := range fv.Interface().([]*jsonschema.Schema) {
					walk(c, fmt.Sprintf("%s/%s/%d", path, name, j))
				}
			case schemaMapType:
				m := fv.Interface().(map[string]*jsonschema.Schema)
				for _, k := range slices.Sorted(maps.Keys(m)) {
					walk(m[k], path+"/"+name+"/"+k)
				}
			}
		}
	}
	walk(root, "#")
	return order, paths
}

// goOnlyNames are the schema fields jsonschema-go splits out of one JSON
// keyword (json:"-"), named by that keyword.
var goOnlyNames = map[string]string{
	"DependencySchemas": "dependencies",
	"Items":             "items",
	"ItemsArray":        "items",
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" || name == "-" {
		if n, ok := goOnlyNames[f.Name]; ok {
			return n
		}
		return f.Name
	}
	return name
}

var (
	schemaType      = reflect.TypeFor[*jsonschema.Schema]()
	schemaSliceType = reflect.TypeFor[[]*jsonschema.Schema]()
	schemaMapType   = reflect.TypeFor[map[string]*jsonschema.Schema]()
)

// refTargets is what one schema's $ref and $dynamicRef resolved to.
type refTargets struct {
	targets       []*jsonschema.Schema // the lexical targets
	dynamicAnchor string               // set when the $dynamicRef resolves at validation time
}

// resolvedTargets reads, without modifying anything, the reference targets
// jsonschema-go recorded during Resolve. They are unexported in v0.4.3
// (Resolved.resolvedInfos[schema].resolvedRef / resolvedDynamicRef /
// dynamicRefAnchor), so this uses read-only reflection. TestResolvedLayout
// fails if a library upgrade changes that layout, and any mismatch at run
// time is an error, never a silently missing edge.
func resolvedTargets(rs *jsonschema.Resolved, nodes map[uintptr]*jsonschema.Schema) (map[*jsonschema.Schema]refTargets, error) {
	infos := reflect.ValueOf(rs).Elem().FieldByName("resolvedInfos")
	if !infos.IsValid() || infos.Kind() != reflect.Map || infos.Type().Key() != schemaType {
		return nil, errors.New("jsonschema.Resolved has no resolvedInfos map keyed by schema")
	}
	out := map[*jsonschema.Schema]refTargets{}
	iter := infos.MapRange()
	for iter.Next() {
		key, info := iter.Key(), iter.Value()
		if info.Kind() != reflect.Pointer || info.IsNil() || info.Elem().Kind() != reflect.Struct {
			return nil, errors.New("unexpected resolvedInfos entry")
		}
		s := nodes[key.Pointer()]
		if s == nil {
			continue // a schema outside the tree, such as a meta-schema
		}
		info = info.Elem()
		ref := info.FieldByName("resolvedRef")
		dyn := info.FieldByName("resolvedDynamicRef")
		anchor := info.FieldByName("dynamicRefAnchor")
		if !ref.IsValid() || !dyn.IsValid() || !anchor.IsValid() ||
			ref.Type() != schemaType || dyn.Type() != schemaType || anchor.Kind() != reflect.String {
			return nil, errors.New("resolvedInfo layout changed")
		}
		var t refTargets
		if s.Ref != "" {
			target := targetOf(ref, nodes)
			if target == nil {
				return nil, fmt.Errorf("no resolved target for $ref %q", s.Ref)
			}
			t.targets = append(t.targets, target)
		}
		if s.DynamicRef != "" {
			t.dynamicAnchor = anchor.String()
			if target := targetOf(dyn, nodes); target != nil {
				t.targets = append(t.targets, target)
			} else if t.dynamicAnchor == "" {
				return nil, fmt.Errorf("no resolved target for $dynamicRef %q", s.DynamicRef)
			}
		}
		if len(t.targets) > 0 || t.dynamicAnchor != "" {
			out[s] = t
		}
	}
	for _, s := range nodes {
		if _, ok := out[s]; !ok && (s.Ref != "" || s.DynamicRef != "") {
			return nil, errors.New("a reference has no resolved target")
		}
	}
	return out, nil
}

func targetOf(v reflect.Value, nodes map[uintptr]*jsonschema.Schema) *jsonschema.Schema {
	if v.IsNil() {
		return nil
	}
	return nodes[v.Pointer()]
}
