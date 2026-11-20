package elicitation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// MaxSchemaBytes bounds a schema the model writes itself (a subtask's
// return_spec, an ask_user question). Tool schemas from MCP servers are not
// held to it.
const MaxSchemaBytes = 16 << 10

// ResolveLimited is Resolve for a schema the model wrote: it also refuses
// one over MaxSchemaBytes.
func ResolveLimited(raw json.RawMessage) (*jsonschema.Resolved, error) {
	if len(raw) > MaxSchemaBytes {
		return nil, fmt.Errorf("schema is %d bytes; the limit is %d", len(raw), MaxSchemaBytes)
	}
	return Resolve(raw)
}

// checkRefs refuses a schema whose references could make validation recurse
// without end, which in jsonschema-go overflows the stack and kills the
// process. A $ref may only point inside the document ("#" or "#/..."), and
// following $ref, allOf, anyOf, oneOf, not, if/then/else and
// dependentSchemas (the keywords that apply to the same value) must never
// come back to a schema already on the path. Recursion through properties,
// items and the like is fine: each step moves into a smaller value.
// $dynamicRef and $recursiveRef are refused outright.
func checkRefs(raw json.RawMessage) error {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	nodes := map[string]map[string]any{}
	collect(doc, "#", nodes)
	edges := map[string][]string{}
	for ptr, node := range nodes {
		for _, key := range []string{"$dynamicRef", "$recursiveRef"} {
			if _, ok := node[key]; ok {
				return fmt.Errorf("schema uses %s, which is not supported", key)
			}
		}
		if ref, ok := node["$ref"].(string); ok {
			if ref != "#" && !strings.HasPrefix(ref, "#/") {
				return fmt.Errorf("schema $ref %q: only references within the schema (#/...) are supported", ref)
			}
			target := normalize(ref)
			if _, ok := nodes[target]; !ok {
				return fmt.Errorf("schema $ref %q points at nothing", ref)
			}
			edges[ptr] = append(edges[ptr], target)
		}
		for _, key := range []string{"allOf", "anyOf", "oneOf"} {
			if list, ok := node[key].([]any); ok {
				for i := range list {
					edges[ptr] = append(edges[ptr], ptr+"/"+key+"/"+strconv.Itoa(i))
				}
			}
		}
		for _, key := range []string{"not", "if", "then", "else"} {
			if _, ok := node[key].(map[string]any); ok {
				edges[ptr] = append(edges[ptr], ptr+"/"+key)
			}
		}
		if deps, ok := node["dependentSchemas"].(map[string]any); ok {
			for name := range deps {
				edges[ptr] = append(edges[ptr], ptr+"/dependentSchemas/"+escape(name))
			}
		}
	}
	const (
		unseen = iota
		onPath
		done
	)
	state := map[string]int{}
	var visit func(string) error
	visit = func(ptr string) error {
		switch state[ptr] {
		case onPath:
			return fmt.Errorf("schema refers to itself at %s without descending into a value; validation would never end", ptr)
		case done:
			return nil
		}
		state[ptr] = onPath
		for _, next := range edges[ptr] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[ptr] = done
		return nil
	}
	for ptr := range nodes {
		if err := visit(ptr); err != nil {
			return err
		}
	}
	return nil
}

// collect records every object in the document by its JSON pointer, skipping
// values that are data rather than schemas.
func collect(value any, ptr string, nodes map[string]map[string]any) {
	switch v := value.(type) {
	case map[string]any:
		nodes[ptr] = v
		for key, child := range v {
			switch key {
			case "enum", "const", "default", "examples":
				continue
			}
			collect(child, ptr+"/"+escape(key), nodes)
		}
	case []any:
		for i, child := range v {
			collect(child, ptr+"/"+strconv.Itoa(i), nodes)
		}
	}
}

func escape(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

// normalize turns a $ref into the pointer form collect uses.
func normalize(ref string) string {
	if ref == "#" || ref == "#/" {
		return "#"
	}
	return strings.TrimSuffix(ref, "/")
}
