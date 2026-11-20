// Copyright 2025 The JSON Schema Go Project Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Tests for the Nanite patch (budget.go).

package jsonschema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func resolveJSON(t *testing.T, raw string) *Resolved {
	t.Helper()
	var s Schema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	rs, err := s.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func decode(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBudgetStopsWork(t *testing.T) {
	rs := resolveJSON(t, `{"allOf":[{"type":"array","items":{"$ref":"#"}},{"type":"array","items":{"$ref":"#"}}]}`)
	deep := strings.Repeat("[", 30) + strings.Repeat("]", 30)
	if err := rs.Validate(context.Background(), decode(t, deep), Budget{Visits: 1000}); !errors.Is(err, ErrBudget) {
		t.Fatalf("err = %v", err)
	}
	if err := rs.Validate(context.Background(), decode(t, "[[[]]]"), Budget{}); err != nil {
		t.Fatalf("small value: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rs.Validate(ctx, decode(t, deep), Budget{Visits: 1 << 40}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestBudgetChargesProportionally(t *testing.T) {
	big := "[" + strings.TrimSuffix(strings.Repeat("0,", 10000), ",") + "]"
	for name, tc := range map[string]struct{ schema, value string }{
		"enum walk":         {`{"enum":[` + big + `]}`, big},
		"const walk":        {`{"const":` + big + `}`, big},
		"uniqueItems size":  {`{"uniqueItems":true}`, "[" + big + "," + big + "]"},
		"minLength":         {`{"minLength":1}`, `"` + strings.Repeat("x", 1<<20) + `"`},
		"required":          {`{"required":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 5000), ",") + `]}`, `{"a":1}`},
		"additional false":  {`{"additionalProperties":false}`, "{" + strings.TrimSuffix(strings.Repeat(`"a":1,`, 1), ",") + "}"},
		"pattern by length": {`{"pattern":"^a*$"}`, `"` + strings.Repeat("a", 1<<20) + `"`},
	} {
		t.Run(name, func(t *testing.T) {
			rs := resolveJSON(t, tc.schema)
			err := rs.Validate(context.Background(), decode(t, tc.value), Budget{Visits: 1000})
			if name == "additional false" {
				// One key: cheap, and the verdict is unchanged.
				if err == nil || errors.Is(err, ErrBudget) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrBudget) {
				t.Fatalf("err = %v, want the budget spent", err)
			}
		})
	}
}

// st.equal must agree with upstream's equalValue.
func TestEqualMatchesUpstream(t *testing.T) {
	values := []string{`null`, `true`, `false`, `0`, `1`, `1.0`, `"a"`, `""`, `[]`, `[1]`, `[1,2]`, `[2,1]`,
		`{}`, `{"a":1}`, `{"a":1.0}`, `{"a":2}`, `{"b":1}`, `{"a":[1,{"b":null}]}`, `{"a":[1,{"b":false}]}`}
	st := &state{}
	st.init(context.Background(), Budget{Visits: 1 << 30})
	for _, a := range values {
		for _, b := range values {
			x, y := reflect.ValueOf(decode(t, a)), reflect.ValueOf(decode(t, b))
			got, err := st.equal(x, y)
			if err != nil {
				t.Fatal(err)
			}
			if want := equalValue(x, y); got != want {
				t.Errorf("equal(%s, %s) = %v, upstream %v", a, b, got, want)
			}
		}
	}
}

func TestSmallerSideKeepsTheResult(t *testing.T) {
	rs := resolveJSON(t, `{"properties":{"a":{"type":"string"},"b":{"type":"string"},"c":{"type":"string"}}}`)
	for value, ok := range map[string]bool{`{"a":"x"}`: true, `{"a":1}`: false, `{"z":1,"y":2,"x":3,"w":4}`: true, `{"c":5,"y":2,"x":3,"w":4}`: false} {
		err := rs.Validate(context.Background(), decode(t, value), Budget{})
		if (err == nil) != ok {
			t.Errorf("%s: %v", value, err)
		}
	}
}

func TestBoundedFormatting(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := fmt.Sprintf("%v", bounded{long}); len(got) > maxShownValue+len("…") {
		t.Fatalf("%d bytes", len(got))
	}
	if got := fmt.Sprintf("%q", bounded{"a"}); got != `"a"` {
		t.Fatalf("%%q = %s", got)
	}
	st := &state{}
	st.init(context.Background(), Budget{})
	err := st.errorf("type: %v of type %[1]T is not valid; %T and %d", 3, "s", 4)
	if got := err.Error(); got != "type: 3 of type int is not valid; string and 4" {
		t.Fatalf("errorf = %q", got)
	}
}

func TestWrapChargesOnlyThePrefix(t *testing.T) {
	schema, value := `{"type":"string"}`, `5`
	for i := range 20 {
		schema = fmt.Sprintf(`{"properties":{"f%d":%s}}`, i, schema)
		value = fmt.Sprintf(`{"f%d":%s}`, i, value)
	}
	rs := resolveJSON(t, schema)
	// Enough for the prefixes and the leaf, not for 20 copies of the text.
	err := rs.Validate(context.Background(), decode(t, value), Budget{ErrorBytes: 4 << 10})
	if err == nil || !strings.HasSuffix(err.Error(), `type: 5 has type "integer", want "string"`) || !strings.HasPrefix(err.Error(), "validating root") {
		t.Fatalf("err = %.300v", err)
	}
	// Once the budget is spent, the inner error is kept as it is.
	err = rs.Validate(context.Background(), decode(t, value), Budget{ErrorBytes: 1})
	if err == nil || !strings.Contains(err.Error(), `has type "integer"`) {
		t.Fatalf("spent budget: %v", err)
	}
}

func TestDiscardedBranchesBuildNoText(t *testing.T) {
	// Each failing oneOf branch would otherwise spend the error budget.
	alts := strings.TrimSuffix(strings.Repeat(`{"type":"string","minLength":5},`, 200), ",")
	rs := resolveJSON(t, `{"properties":{"x":{"oneOf":[`+alts+`]},"y":{"type":"integer"}}}`)
	err := rs.Validate(context.Background(), decode(t, `{"x":"a","y":"no"}`), Budget{ErrorBytes: 2 << 10})
	if err == nil || strings.Contains(err.Error(), "further detail omitted") {
		t.Fatalf("err = %v", err)
	}
}

// The budget alone (no deadline) stops each of these: each was charged one
// unit for unbounded work.
func TestBudgetAloneStopsKeyLoopsAndFormatting(t *testing.T) {
	keys := func(value string) string {
		parts := make([]string, 20000)
		for i := range parts {
			parts[i] = fmt.Sprintf(`"k%d":%s`, i, value)
		}
		return strings.Join(parts, ",")
	}
	empties := "[" + strings.TrimSuffix(strings.Repeat("{},", 150000), ",") + "]"
	ints := make([]string, 20000)
	for i := range ints {
		ints[i] = fmt.Sprint(i)
	}
	for name, tc := range map[string]struct{ schema, value string }{
		"dependentRequired":     {`{"type":"array","items":{"dependentRequired":{` + keys("[]") + `}}}`, empties},
		"dependentSchemas":      {`{"type":"array","items":{"dependentSchemas":{` + keys("true") + `}}}`, empties},
		"draft-07 dependencies": {`{"$schema":"http://json-schema.org/draft-07/schema#","type":"array","items":{"dependencies":{` + keys(`["a"]`) + `}}}`, empties},
		"formatting refunded by anyOf": {`{"allOf":[` + strings.TrimSuffix(strings.Repeat(`{"$ref":"#/$defs/A"},`, 550), ",") +
			`],"$defs":{"A":{"anyOf":[` + strings.Repeat(`{"type":"integer"},`, 15) + `{}]}}}`,
			`{"blob":"` + strings.Repeat("x", 400000) + `","arr":[` + strings.Join(ints, ",") + `]}`},
	} {
		t.Run(name, func(t *testing.T) {
			rs := resolveJSON(t, tc.schema)
			v := decode(t, tc.value)
			start := time.Now()
			err := rs.Validate(context.Background(), v, Budget{})
			if took := time.Since(start); took > 2*time.Second {
				t.Fatalf("took %s: %v", took, err)
			}
			// Empty objects match no keys, so the key loops may finish
			// valid within budget; the formatting case must hit it.
			if name == "formatting refunded by anyOf" && !errors.Is(err, ErrBudget) {
				t.Fatalf("err = %.200v", err)
			}
		})
	}
}
