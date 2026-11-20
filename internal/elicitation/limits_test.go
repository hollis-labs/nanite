package elicitation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// reproCase is a schema and value that killed, hung or exhausted a process
// before these limits (each found in review of CW-20261120-0010).
type reproCase struct {
	schema, value string
	want          string // the outcome line the child prints
}

func defs(n int, body func(i int) string) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = fmt.Sprintf(`"a%d":%s`, i, body(i))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func reproCases() map[string]reproCase {
	ref := func(i int) string { return fmt.Sprintf(`{"$ref":"#/$defs/a%d"}`, i) }
	// Width-2 anyOf DAG: 2^25 paths. Exhausted memory.
	anyOfDAG := defs(25, func(i int) string {
		if i == 24 {
			return `{}`
		}
		return fmt.Sprintf(`{"anyOf":[%s,%s]}`, ref(i+1), ref(i+1))
	})
	// Width-2 allOf DAG: exponential time.
	allOfDAG := defs(23, func(i int) string {
		if i == 22 {
			return `{}`
		}
		return fmt.Sprintf(`{"allOf":[%s,%s]}`, ref(i+1), ref(i+1))
	})
	// Width-8 anyOf DAG, 7 levels: short enough for the chain limit, but
	// 8^7 ways through it. Caught by the path count.
	wideDAG := defs(8, func(i int) string {
		if i == 7 {
			return `{}`
		}
		alts := make([]string, 8)
		for j := range alts {
			alts[j] = ref(i + 1)
		}
		return `{"anyOf":[` + strings.Join(alts, ",") + `]}`
	})
	// allOf chain of 6, then items back to the start: acyclic in place, but a
	// 9,990-deep value overflowed the stack.
	itemsChain := defs(7, func(i int) string {
		if i == 6 {
			return `{"items":` + ref(0) + `}`
		}
		return `{"allOf":[` + ref(i+1) + `]}`
	})
	deep := strings.Repeat("[", 9990) + strings.Repeat("]", 9990)
	// A 200,000-long $ref chain: stack overflow in Resolve or Validate.
	refChain := defs(200000, func(i int) string {
		if i == 199999 {
			return `{}`
		}
		return ref(i + 1)
	})
	// 40,000 $dynamicRefs to one anchor: quadratic work in the check.
	fan := func(n int) string {
		props := make([]string, n)
		for i := range n {
			props[i] = fmt.Sprintf(`"p%d":{"$dynamicRef":"#n"}`, i)
		}
		return `{"$dynamicAnchor":"n","type":"object","properties":{` + strings.Join(props, ",") + `}}`
	}
	return map[string]reproCase{
		"anyOf DAG (OOM)":          {`{"type":"object","properties":{"x":{"$ref":"#/$defs/a0"}},"$defs":` + anyOfDAG + `}`, `{"x":{}}`, "refused"},
		"wide anyOf DAG (paths)":   {`{"$ref":"#/$defs/a0","$defs":` + wideDAG + `}`, `1`, "refused: paths"},
		"allOf DAG (time)":         {`{"$ref":"#/$defs/a0","$defs":` + allOfDAG + `}`, `1`, "refused"},
		"items chain, deep value":  {`{"type":"object","properties":{"x":{"$ref":"#/$defs/a0"}},"$defs":` + itemsChain + `}`, `{"x":` + deep + `}`, "too deep"},
		"200,000 $ref chain":       {`{"$ref":"#/$defs/a0","$defs":` + refChain + `}`, `1`, "refused"},
		"40,000 $dynamicRef fan":   {fan(40000), `{"p0":{}}`, "refused"},
		"6,000 $dynamicRef fan ok": {fan(6000), `{"p0":{}}`, "valid"},
	}
}

// TestReproChild is the child half of TestReprosRunInAChildProcess: it runs
// one case and prints the outcome. A crash, OOM or hang fails the parent.
func TestReproChild(t *testing.T) {
	name := os.Getenv("NANITE_SCHEMA_REPRO")
	if name == "" {
		t.Skip("run by TestReprosRunInAChildProcess")
	}
	tc := reproCases()[name]
	resolved, err := Resolve(json.RawMessage(tc.schema))
	if err != nil {
		if !errors.Is(err, ErrUnsafeSchema) {
			t.Fatalf("Resolve: %v", err)
		}
		if strings.Contains(err.Error(), "ways to apply") {
			fmt.Println("OUTCOME: refused: paths")
			return
		}
		fmt.Println("OUTCOME: refused")
		return
	}
	var value any
	if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
		t.Fatal(err)
	}
	switch err := Validate(resolved, value); {
	case errors.Is(err, ErrValueTooDeep):
		fmt.Println("OUTCOME: too deep")
	case err != nil:
		fmt.Println("OUTCOME: invalid")
	default:
		fmt.Println("OUTCOME: valid")
	}
}

func TestReprosRunInAChildProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	for name, tc := range reproCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReproChild$", "-test.count=1") //nolint:gosec // re-runs this test binary
			cmd.Env = append(os.Environ(), "NANITE_SCHEMA_REPRO="+name)
			start := time.Now()
			out, err := cmd.CombinedOutput()
			took := time.Since(start)
			if err != nil {
				t.Fatalf("child failed after %s: %v\n%s", took, err, tail(string(out)))
			}
			if !strings.Contains(string(out), "OUTCOME: "+tc.want+"\n") {
				t.Fatalf("want %q, got:\n%s", tc.want, tail(string(out)))
			}
			if took > time.Minute {
				t.Fatalf("took %s", took)
			}
		})
	}
}

func tail(s string) string {
	if len(s) > 2000 {
		return "…" + s[len(s)-2000:]
	}
	return s
}

func TestLimits(t *testing.T) {
	chain := func(n int) string {
		return `{"$ref":"#/$defs/a0","$defs":` + defs(n, func(i int) string {
			if i == n-1 {
				return `{}`
			}
			return fmt.Sprintf(`{"$ref":"#/$defs/a%d"}`, i+1)
		}) + `}`
	}
	nestedNot := func(n int) string { return strings.Repeat(`{"not":`, n) + `{}` + strings.Repeat(`}`, n) }
	for name, tc := range map[string]struct {
		schema string
		want   string // "" when it resolves
	}{
		"chain at the limit":        {chain(MaxInPlaceChain - 1), ""},
		"chain past the limit":      {chain(MaxInPlaceChain + 1), "in a row"},
		"inline nesting past limit": {nestedNot(MaxInPlaceChain + 1), "in a row"},
		"too large":                 {`{"description":"` + strings.Repeat("x", MaxToolSchemaBytes) + `"}`, "bytes"},
		"nested too deeply":         {strings.Repeat(`{"properties":{"a":`, MaxSchemaNesting) + `{}` + strings.Repeat(`}}`, MaxSchemaNesting), "nests"},
		"wide anyOf is fine":        {`{"anyOf":[` + strings.TrimSuffix(strings.Repeat(`{"type":"string"},`, 500), ",") + `]}`, ""},
		"too many paths":            {`{"anyOf":[` + strings.TrimSuffix(strings.Repeat(`{"type":"string"},`, MaxInPlacePaths+1), ",") + `]}`, "ways"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(json.RawMessage(tc.schema))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrUnsafeSchema) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateRefusesDeepValuesAndCutsErrors(t *testing.T) {
	resolved, err := Resolve(json.RawMessage(`{"type":"array","items":{"$ref":"#"}}`))
	if err != nil {
		t.Fatal(err)
	}
	nest := func(n int) any {
		var v any = []any{}
		for range n - 1 {
			v = []any{v}
		}
		return v
	}
	if err := Validate(resolved, nest(MaxValueDepth)); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if err := Validate(resolved, nest(MaxValueDepth+2)); !errors.Is(err, ErrValueTooDeep) {
		t.Fatalf("past the limit: %v", err)
	}
	// anyOf error text grows with the alternatives; it is cut.
	alts := make([]string, 400)
	for i := range alts {
		alts[i] = fmt.Sprintf(`{"const":"value number %d, which is long enough to add up"}`, i)
	}
	wide, err := Resolve(json.RawMessage(`{"anyOf":[` + strings.Join(alts, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	err = Validate(wide, "nope")
	if err == nil || len(err.Error()) > MaxValidationError+32 || !strings.HasSuffix(err.Error(), "(truncated)") {
		t.Fatalf("len %d: %.200v", len(err.Error()), err)
	}
}

func TestResolveIsCached(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"cached":{"type":"string"}}}`)
	first, err := Resolve(raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(append(json.RawMessage(nil), raw...))
	if err != nil || first != second {
		t.Fatalf("second resolve %p %p %v", first, second, err)
	}
}
