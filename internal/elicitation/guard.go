package elicitation

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp/syntax"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
)

// jsonschema-go validates by plain recursion and explores every alternative
// of anyOf, allOf and the like, so a schema and value can take unbounded
// time and memory, or overflow the stack (a fatal runtime error that no
// recover catches, so it ends the whole process). Nanite validates with a
// vendored copy (internal/third_party/jsonschema) patched to take a context
// and a work budget, which bounds every call. The static limits below are
// cheap checks made once per schema that refuse what could never validate
// well; the value depth limit keeps recursion off the stack.
const (
	// MaxToolSchemaBytes bounds a tool input schema from a host or MCP
	// server. A schema the model writes is held to MaxSchemaBytes.
	MaxToolSchemaBytes = 256 << 10
	// MaxSchemaNesting bounds how deeply a schema's JSON nests.
	MaxSchemaNesting = 256
	// MaxInPlaceChain bounds the longest run of keywords that apply to the
	// same value ($ref, allOf, anyOf, oneOf, not, if/then/else, ...).
	MaxInPlaceChain = 64
	// MaxInPlacePaths bounds how many such runs start at one schema: the
	// work Validate does for one location in a value.
	MaxInPlacePaths = 10_000
	// MaxValueDepth bounds how deeply a value (tool input, a returned result,
	// an answer) nests before it is validated.
	MaxValueDepth = 64
	// MaxValidationTime bounds the wall time of one Validate call, on top of
	// its work budget.
	MaxValidationTime = 2 * time.Second
	// MaxValidationError bounds the validation error text passed on.
	MaxValidationError = 4 << 10
	// MaxSchemaRegexpInsts bounds the compiled size of all of a schema's
	// patterns together (each is also held to jsonschema.MaxRegexpInsts).
	MaxSchemaRegexpInsts = 100_000
)

// ErrUnsafeSchema marks a schema Nanite will not validate against: one that
// refers back to itself in place, is too large, nests too deeply, or makes
// validation do too much work.
var ErrUnsafeSchema = errors.New("unsafe schema")

// ErrValueTooDeep marks a value nested more than MaxValueDepth levels.
var ErrValueTooDeep = errors.New("value nests too deeply")

// ValidationBudget is the work budget for one Validate call.
var ValidationBudget = jsonschema.Budget{Visits: jsonschema.DefaultVisits, ErrorBytes: MaxValidationError}

// Validate checks value against a resolved schema. A value nested more than
// MaxValueDepth levels is refused before validation; validation stops with
// an error wrapping jsonschema.ErrBudget once it exceeds ValidationBudget, or
// when ctx is done; and the error text is cut to MaxValidationError bytes.
//
// value is a decoded JSON value (map[string]any, []any and scalars), as
// json.Unmarshal into an any produces; other maps and slices are measured
// through reflection.
func Validate(ctx context.Context, schema *jsonschema.Resolved, value any) error {
	if depth(value, 0) > MaxValueDepth {
		return fmt.Errorf("%w: more than %d levels", ErrValueTooDeep, MaxValueDepth)
	}
	ctx, cancel := context.WithTimeout(ctx, MaxValidationTime)
	defer cancel()
	err := schema.Validate(ctx, value, ValidationBudget)
	if err == nil {
		return nil
	}
	if errors.Is(err, jsonschema.ErrBudget) || ctx.Err() != nil {
		return err
	}
	if text := err.Error(); len(text) > MaxValidationError {
		// The innermost failure (the leaf path and what was wrong) is at the
		// end, so keep the start and the end.
		head := cut(text, MaxValidationError/4)
		return errors.New(head + " … " + tailCut(text, MaxValidationError-len(head)-5))
	}
	return err
}

// tailCut is the last at most limit bytes of text, starting on a rune.
func tailCut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && text[start]&0xC0 == 0x80 {
		start++
	}
	return text[start:]
}

// depth is how deeply a JSON value nests, stopping once past the limit so a
// pathological value costs no more than the limit.
func depth(value any, level int) int {
	if level > MaxValueDepth {
		return level
	}
	deepest := level
	visit := func(c any) bool {
		d := depth(c, level+1)
		deepest = max(deepest, d)
		return d <= MaxValueDepth
	}
	switch v := value.(type) {
	case nil, string, bool, float64, json.Number:
	case map[string]any:
		for _, c := range v {
			if !visit(c) {
				break
			}
		}
	case []any:
		for _, c := range v {
			if !visit(c) {
				break
			}
		}
	default:
		rv := reflect.ValueOf(value)
		for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
			if rv.IsNil() {
				return deepest
			}
			rv = rv.Elem()
		}
		switch rv.Kind() { //nolint:exhaustive // only containers nest
		case reflect.Map:
			for it := rv.MapRange(); it.Next(); {
				if !visit(it.Value().Interface()) {
					break
				}
			}
		case reflect.Slice, reflect.Array:
			for i := range rv.Len() {
				if !visit(rv.Index(i).Interface()) {
					break
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

// resolveCache keeps resolved schemas by content, least recently used first
// out, so a tool's schema is resolved once rather than on every turn. An
// entry's size is estimated from its schema text and bounded in total by
// resolveCacheBytes. A Resolved is read-only and safe to share between
// goroutines.
var resolveCache = struct {
	sync.Mutex
	order   *list.List // of *resolvedEntry, most recent first
	entries map[[sha256.Size]byte]*list.Element
	bytes   int
}{order: list.New(), entries: map[[sha256.Size]byte]*list.Element{}}

type resolvedEntry struct {
	key    [sha256.Size]byte
	schema *jsonschema.Resolved
	err    error
	cost   int
}

const (
	resolveCacheBytes = 64 << 20
	// A resolved schema holds up to about 45 bytes per byte of schema text (the
	// parsed tree, per-schema resolution data and compiled patterns), plus a
	// few KB, measured on webhook-union, inheritance-chain, Kubernetes-style
	// and small schemas; TestCacheCostEstimate checks it. A refused schema
	// holds only its error.
	resolveFixedCost   = 4 << 10
	resolveCostPerByte = 52
	refusedCost        = 512
)

func entryCost(raw []byte, schema *jsonschema.Resolved) int {
	if schema == nil {
		return refusedCost
	}
	return resolveFixedCost + resolveCostPerByte*len(raw)
}

func cachedResolve(raw []byte, resolve func() (*jsonschema.Resolved, error)) (*jsonschema.Resolved, error) {
	key := sha256.Sum256(raw)
	resolveCache.Lock()
	if el, ok := resolveCache.entries[key]; ok {
		resolveCache.order.MoveToFront(el)
		entry := el.Value.(*resolvedEntry)
		resolveCache.Unlock()
		return entry.schema, entry.err
	}
	resolveCache.Unlock()
	schema, err := resolve()
	entry := &resolvedEntry{key: key, schema: schema, err: err, cost: entryCost(raw, schema)}
	if entry.cost > resolveCacheBytes {
		return schema, err
	}
	resolveCache.Lock()
	defer resolveCache.Unlock()
	if _, ok := resolveCache.entries[key]; ok {
		return schema, err
	}
	resolveCache.entries[key] = resolveCache.order.PushFront(entry)
	resolveCache.bytes += entry.cost
	for resolveCache.bytes > resolveCacheBytes {
		last := resolveCache.order.Back()
		old := last.Value.(*resolvedEntry)
		resolveCache.order.Remove(last)
		delete(resolveCache.entries, old.key)
		resolveCache.bytes -= old.cost
	}
	return schema, err
}

// checkPatterns refuses a schema whose patterns together compile to more than
// MaxSchemaRegexpInsts instructions, before the library compiles any of them.
// It stops counting at the limit, so a schema with thousands of large
// patterns costs no more than the limit.
func checkPatterns(root *jsonschema.Schema) error {
	order, paths := index(root)
	total := 0
	count := func(pattern, where string) error {
		// A pattern that does not parse or compile adds nothing here; the
		// library refuses it when it resolves the schema.
		total += regexpInsts(pattern)
		if total > MaxSchemaRegexpInsts {
			return fmt.Errorf("%w: its patterns compile to more than %d instructions together (at %s)", ErrUnsafeSchema, MaxSchemaRegexpInsts, where)
		}
		return nil
	}
	for _, s := range order {
		if s.Pattern != "" {
			if err := count(s.Pattern, paths[s]+"/pattern"); err != nil {
				return err
			}
		}
		for _, p := range slices.Sorted(maps.Keys(s.PatternProperties)) {
			if err := count(p, paths[s]+"/patternProperties"); err != nil {
				return err
			}
		}
	}
	return nil
}

// regexpInsts is the compiled size of a pattern, or 0 if it does not compile.
func regexpInsts(pattern string) int {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return 0
	}
	prog, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return 0
	}
	return len(prog.Inst)
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
	refs, err := resolvedTargets(rs, order, nodes)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnsafeSchema, err)
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

// resolvedTargets is each referring schema's targets, as the library
// resolved them. A reference without a target in the tree is an error.
func resolvedTargets(rs *jsonschema.Resolved, order []*jsonschema.Schema, nodes map[uintptr]*jsonschema.Schema) (map[*jsonschema.Schema]refTargets, error) {
	inTree := func(s *jsonschema.Schema) bool { return s != nil && nodes[reflect.ValueOf(s).Pointer()] == s }
	out := map[*jsonschema.Schema]refTargets{}
	for _, s := range order {
		if s.Ref == "" && s.DynamicRef == "" {
			continue
		}
		ref, dyn, anchor := rs.RefTargets(s)
		var t refTargets
		if s.Ref != "" {
			if !inTree(ref) {
				return nil, fmt.Errorf("no resolved target for $ref %q", s.Ref)
			}
			t.targets = append(t.targets, ref)
		}
		if s.DynamicRef != "" {
			switch {
			case inTree(dyn):
				t.targets = append(t.targets, dyn)
			case anchor != "":
				t.dynamicAnchor = anchor
			default:
				return nil, fmt.Errorf("no resolved target for $dynamicRef %q", s.DynamicRef)
			}
		}
		out[s] = t
	}
	return out, nil
}
