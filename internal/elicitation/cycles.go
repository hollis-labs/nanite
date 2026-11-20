package elicitation

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// ErrCyclicSchema marks a schema that refers back to itself without moving
// into a smaller value. jsonschema-go v0.4.3 validates such a schema by
// recursing without end, which overflows the stack: a fatal runtime error that
// no recover catches, so it would end the whole process.
var ErrCyclicSchema = errors.New("schema refers back to itself in place")

// checkCycles refuses a resolved schema whose in-place keywords form a cycle.
//
// "In place" means the keyword applies its subschemas to the same value: $ref,
// $dynamicRef, allOf, anyOf, oneOf, not, if, then, else, dependentSchemas and
// draft-07 schema dependencies. A cycle of those never terminates. Recursion
// through properties, items and the like is fine: each step is into a smaller
// part of a finite value.
//
// The $ref and $dynamicRef targets are the library's own resolution (its $id
// bases, anchors, percent- and pointer-decoding), read from the Resolved, so
// the check agrees with what Validate will follow. A dynamic reference that
// resolves at validation time is taken to reach every schema with a matching
// $dynamicAnchor, which can only over-report.
func checkCycles(rs *jsonschema.Resolved) error {
	root := rs.Schema()
	if root == nil {
		return nil
	}
	order, paths := index(root)
	nodes := make(map[uintptr]*jsonschema.Schema, len(order))
	for _, s := range order {
		nodes[reflect.ValueOf(s).Pointer()] = s
	}

	refs, err := resolvedTargets(rs)
	if err != nil {
		// The library's internals are not what this check was written
		// against. Refuse only what could recurse through a reference.
		for _, s := range order {
			if s.Ref != "" || s.DynamicRef != "" {
				return fmt.Errorf("%w: cannot check $ref targets: %w", ErrCyclicSchema, err)
			}
		}
		refs = nil
	}

	edges := func(s *jsonschema.Schema) []*jsonschema.Schema {
		var out []*jsonschema.Schema
		if t, ok := refs[reflect.ValueOf(s).Pointer()]; ok {
			for _, p := range t.targets {
				if target := nodes[p]; target != nil {
					out = append(out, target)
				}
			}
			if t.dynamicAnchor != "" {
				for _, n := range order {
					if n.DynamicAnchor == t.dynamicAnchor {
						out = append(out, n)
					}
				}
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
		for _, c := range s.DependentSchemas {
			out = append(out, c)
		}
		for _, c := range s.DependencySchemas {
			out = append(out, c)
		}
		return out
	}

	const (
		unseen = iota
		onPath
		done
	)
	state := map[*jsonschema.Schema]int{}
	var stack []*jsonschema.Schema
	var visit func(*jsonschema.Schema) error
	visit = func(s *jsonschema.Schema) error {
		switch state[s] {
		case onPath:
			var loop []string
			for i := len(stack) - 1; i >= 0; i-- {
				loop = append([]string{paths[stack[i]]}, loop...)
				if stack[i] == s {
					break
				}
			}
			return fmt.Errorf("%w: %s -> %s", ErrCyclicSchema, strings.Join(loop, " -> "), paths[s])
		case done:
			return nil
		}
		state[s] = onPath
		stack = append(stack, s)
		for _, next := range edges(s) {
			if err := visit(next); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[s] = done
		return nil
	}
	// Every subschema can be reached by validation (through properties and
	// the like), so a cycle anywhere counts.
	for _, s := range order {
		if err := visit(s); err != nil {
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

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" || name == "-" {
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
	targets       []uintptr // the lexical targets, by address
	dynamicAnchor string    // set when the $dynamicRef resolves at validation time
}

// resolvedTargets reads, without modifying anything, the reference targets
// jsonschema-go recorded during Resolve. They are unexported in v0.4.3
// (Resolved.resolvedInfos[schema].resolvedRef / resolvedDynamicRef /
// dynamicRefAnchor), so this uses read-only reflection. TestResolvedLayout
// fails if a library upgrade changes that layout.
func resolvedTargets(rs *jsonschema.Resolved) (map[uintptr]refTargets, error) {
	infos := reflect.ValueOf(rs).Elem().FieldByName("resolvedInfos")
	if !infos.IsValid() || infos.Kind() != reflect.Map {
		return nil, errors.New("jsonschema.Resolved has no resolvedInfos map")
	}
	out := map[uintptr]refTargets{}
	iter := infos.MapRange()
	for iter.Next() {
		key, info := iter.Key(), iter.Value()
		if key.Kind() != reflect.Pointer || info.Kind() != reflect.Pointer || info.IsNil() {
			return nil, errors.New("unexpected resolvedInfos entry")
		}
		info = info.Elem()
		ref := info.FieldByName("resolvedRef")
		dyn := info.FieldByName("resolvedDynamicRef")
		anchor := info.FieldByName("dynamicRefAnchor")
		if !ref.IsValid() || !dyn.IsValid() || !anchor.IsValid() ||
			ref.Kind() != reflect.Pointer || dyn.Kind() != reflect.Pointer || anchor.Kind() != reflect.String {
			return nil, errors.New("resolvedInfo layout changed")
		}
		var t refTargets
		if !ref.IsNil() {
			t.targets = append(t.targets, ref.Pointer())
		}
		if !dyn.IsNil() {
			t.targets = append(t.targets, dyn.Pointer())
		}
		t.dynamicAnchor = anchor.String()
		if len(t.targets) > 0 || t.dynamicAnchor != "" {
			out[key.Pointer()] = t
		}
	}
	return out, nil
}
