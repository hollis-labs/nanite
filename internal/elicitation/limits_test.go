package elicitation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
)

// Each case either killed, hung or exhausted a process before the budgeted
// validator (found in review of CW-20261120-0010), or is a realistic schema
// that must keep validating. Every one runs in a child process, which must
// finish within caseWall and caseRSS.
const (
	caseWall = 2 * time.Second
	caseRSS  = 512 << 20
)

type reproCase struct {
	schema, value string
	// want lists the acceptable outcomes: refused, budget, too deep, valid,
	// invalid.
	want []string
}

func defs(n int, body func(i int) string) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = fmt.Sprintf(`"a%d":%s`, i, body(i))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func repeat(n int, item string) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = item
	}
	return strings.Join(parts, ",")
}

var harmless = []string{"refused", "budget", "too deep"}

func reproCases() map[string]reproCase {
	ref := func(i int) string { return fmt.Sprintf(`{"$ref":"#/$defs/a%d"}`, i) }
	dag := func(kw string, width, levels int) string {
		return `{"$ref":"#/$defs/a0","$defs":` + defs(levels, func(i int) string {
			if i == levels-1 {
				return `{}`
			}
			return `{"` + kw + `":[` + repeat(width, ref(i+1)) + `]}`
		}) + `}`
	}
	nested := func(d int, leaf string) string { return strings.Repeat("[", d) + leaf + strings.Repeat("]", d) }
	mult := func(kw string, width int) string {
		return `{"` + kw + `":[` + repeat(width, `{"type":"array","items":{"$ref":"#"}}`) + `]}`
	}
	itemsChain := `{"type":"object","properties":{"x":{"$ref":"#/$defs/a0"}},"$defs":` + defs(7, func(i int) string {
		if i == 6 {
			return `{"items":` + ref(0) + `}`
		}
		return `{"allOf":[` + ref(i+1) + `]}`
	}) + `}`
	refChain := `{"$ref":"#/$defs/a0","$defs":` + defs(200000, func(i int) string {
		if i == 199999 {
			return `{}`
		}
		return ref(i + 1)
	}) + `}`
	fan := func(n int) string {
		props := make([]string, n)
		for i := range n {
			props[i] = fmt.Sprintf(`"p%d":{"$dynamicRef":"#n"}`, i)
		}
		return `{"$dynamicAnchor":"n","type":"object","properties":{` + strings.Join(props, ",") + `}}`
	}
	enum := make([]string, 23000)
	for i := range enum {
		enum[i] = fmt.Sprintf(`"v%06d"`, i)
	}
	rxAnyOf := make([]string, 30)
	for i := range rxAnyOf {
		rxAnyOf[i] = fmt.Sprintf(`{"type":"string","pattern":"[\\p{L}\\p{N}]{1000}!%d"}`, i)
	}
	rxMany := make([]string, 3076)
	for i := range rxMany {
		rxMany[i] = fmt.Sprintf(`"(?:[a-z]{1,100}){1,10}x%d|((a|b|c|d|e)+[0-9]{0,999})%d":{"type":"string"}`, i, i)
	}
	rxKeys := make([]string, 2000)
	for i := range rxKeys {
		rxKeys[i] = fmt.Sprintf(`"k%d%s":"v"`, i, strings.Repeat("a", 200))
	}

	cases := map[string]reproCase{
		// Static limits.
		"anyOf DAG 2x25": {`{"type":"object","properties":{"x":` + ref(0) + `},"$defs":` + defs(25, func(i int) string {
			return map[bool]string{true: `{}`, false: `{"anyOf":[` + repeat(2, ref(i+1)) + `]}`}[i == 24]
		}) + `}`, `{"x":{}}`, harmless},
		"wide anyOf DAG 8x8":     {dag("anyOf", 8, 8), `1`, harmless},
		"allOf DAG 2x23":         {dag("allOf", 2, 23), `1`, harmless},
		"items chain, deep":      {itemsChain, `{"x":` + nested(9990, "") + `}`, harmless},
		"200,000 $ref chain":     {refChain, `1`, harmless},
		"40,000 $dynamicRef fan": {fan(40000), `{"p0":{}}`, harmless},
		// The budget: multiplication across value levels.
		"mult allOf 2 d20": {mult("allOf", 2), nested(20, ""), harmless},
		"mult allOf 2 d24": {mult("allOf", 2), nested(24, ""), harmless},
		"mult allOf 2 d60": {mult("allOf", 2), nested(60, ""), harmless},
		"mult anyOf 2 d18": {mult("anyOf", 2), nested(18, "1"), harmless},
		"mult anyOf 2 d20": {mult("anyOf", 2), nested(20, "1"), harmless},
		"mult anyOf 30 d5": {mult("anyOf", 30), nested(5, "1"), harmless},
		// Error text: input size times branches.
		"wide 999 x 500KB": {`{"anyOf":[` + repeat(999, `{"type":"integer"}`) + `]}`, `{"content":"` + strings.Repeat("x", 500<<10) + `"}`, append([]string{"invalid"}, harmless...)},
		"big enum":         {`{"anyOf":[` + repeat(499, `{"$ref":"#/$defs/E"}`) + `],"$defs":{"E":{"enum":[` + strings.Join(enum, ",") + `]}}}`, `"nope"`, append([]string{"invalid"}, harmless...)},
		"big const":        {`{"anyOf":[` + repeat(499, `{"$ref":"#/$defs/E"}`) + `],"$defs":{"E":{"const":{"k":"` + strings.Repeat("x", 240<<10) + `"}}}}`, `"nope"`, append([]string{"invalid"}, harmless...)},
		// Regular expressions.
		"regex anyOf 30": {`{"type":"object","properties":{"text":{"anyOf":[` + strings.Join(rxAnyOf, ",") + `]}}}`, `{"text":"` + strings.Repeat("a", 100000) + `"}`, harmless},
		"regex many":     {`{"type":"object","patternProperties":{` + strings.Join(rxMany, ",") + `}}`, `{` + strings.Join(rxKeys, ",") + `}`, harmless},
	}
	for name, tc := range realCases() {
		cases[name] = tc
	}
	return cases
}

// realCases are realistic schemas in the shapes of the reviewer's
// scratchpad/real set: they must validate correctly, within budget.
func realCases() map[string]reproCase {
	// A 300-event webhook union, each event an allOf of a shared base.
	events := make([]string, 300)
	evDefs := make([]string, 300)
	fields := make([]string, 6)
	for j := range fields {
		fields[j] = fmt.Sprintf(`"f%d":{"anyOf":[{"type":"string"},{"type":"null"}],"description":"x"}`, j)
	}
	for i := range events {
		events[i] = fmt.Sprintf(`{"$ref":"#/$defs/ev%d"}`, i)
		evDefs[i] = fmt.Sprintf(`"ev%d":{"allOf":[{"$ref":"#/$defs/base"},{"type":"object","properties":{"action":{"const":"a%d"},"payload":{"type":"object","properties":{%s}}}}]}`, i, i, strings.Join(fields, ","))
	}
	webhook := `{"type":"object","properties":{"event":{"oneOf":[` + strings.Join(events, ",") + `]}},"$defs":{` + strings.Join(evDefs, ",") +
		`,"base":{"type":"object","properties":{"sender":{"$ref":"#/$defs/user"},"repository":{"$ref":"#/$defs/repo"},"installation":{"anyOf":[{"$ref":"#/$defs/inst"},{"type":"null"}]}}}` +
		`,"user":{"type":"object","properties":{"login":{"type":"string"}}},"repo":{"type":"object","properties":{"name":{"type":"string"}}},"inst":{"type":"object","properties":{"id":{"type":"integer"}}}}}`
	// A 20-deep allOf inheritance chain.
	chain := make([]string, 20)
	props := make([]string, 20)
	for i := range chain {
		next := `{}`
		if i < 19 {
			next = fmt.Sprintf(`{"$ref":"#/$defs/T%d"}`, i+1)
		}
		chain[i] = fmt.Sprintf(`"T%d":{"allOf":[%s,{"type":"object","properties":{"p%d":{"type":"string"}}}]}`, i, next, i)
		props[i] = fmt.Sprintf(`"p%d":"v"`, i)
	}
	azure := `{"type":"object","properties":{"resource":{"$ref":"#/$defs/T0"}},"$defs":{` + strings.Join(chain, ",") + `}}`
	// Deeply nested objects and arrays with descriptions, as Kubernetes
	// OpenAPI schemas carry.
	k8s := `{"type":"string"}`
	value := `"leaf"`
	for i := range 8 {
		if i%2 == 0 {
			k8s = fmt.Sprintf(`{"type":"object","description":"Description.","properties":{"field%d":%s,"other%d":{"type":"integer"}}}`, i, k8s, i)
			value = fmt.Sprintf(`{"field%d":%s,"other%d":1}`, i, value, i)
		} else {
			k8s = fmt.Sprintf(`{"type":"array","description":"Description.","items":%s}`, k8s)
			value = `[` + value + `,` + value + `]`
		}
	}
	return map[string]reproCase{
		"real: webhook union, valid":    {webhook, `{"event":{"action":"a7","payload":{"f0":"x","f1":null},"sender":{"login":"me"}}}`, []string{"valid"}},
		"real: webhook union, invalid":  {webhook, `{"event":{"action":"a7","payload":{"f0":1}}}`, []string{"invalid"}},
		"real: allOf chain 20, valid":   {azure, `{"resource":{` + strings.Join(props, ",") + `}}`, []string{"valid"}},
		"real: allOf chain 20, invalid": {azure, `{"resource":{"p13":1}}`, []string{"invalid"}},
		"real: nested, valid":           {`{"type":"object","properties":{"spec":` + k8s + `}}`, `{"spec":` + value + `}`, []string{"valid"}},
	}
}

// runCase resolves and validates, and prints the outcome, the time taken and
// the peak resident memory for the parent to check.
func runCase(schema, value []byte) {
	start := time.Now()
	outcome := "valid"
	resolved, err := Resolve(schema)
	switch {
	case errors.Is(err, ErrUnsafeSchema):
		outcome = "refused"
	case err != nil:
		outcome = "invalid schema"
		fmt.Printf("DETAIL: %.300s\n", err)
	default:
		var v any
		if err := json.Unmarshal(value, &v); err != nil {
			outcome = "bad value: " + err.Error()
			break
		}
		switch err := Validate(context.Background(), resolved, v); {
		case errors.Is(err, ErrValueTooDeep):
			outcome = "too deep"
		case errors.Is(err, jsonschema.ErrBudget):
			outcome = "budget"
		case err != nil:
			outcome = "invalid"
			if len(err.Error()) > MaxValidationError+64 {
				outcome = fmt.Sprintf("error text %d bytes", len(err.Error()))
			}
		}
	}
	fmt.Printf("OUTCOME: %s\nMS: %d\nRSS: %d\n", outcome, time.Since(start).Milliseconds(), maxRSS())
}

func maxRSS() int64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return ru.Maxrss // bytes
	}
	return ru.Maxrss << 10 // kilobytes
}

// TestReproChild is the child half of the tests below.
func TestReproChild(t *testing.T) {
	if name := os.Getenv("NANITE_SCHEMA_REPRO"); name != "" {
		tc := reproCases()[name]
		runCase([]byte(tc.schema), []byte(tc.value))
		return
	}
	if path := os.Getenv("NANITE_SCHEMA_FILE"); path != "" {
		schema, err := os.ReadFile(path) //nolint:gosec // a test input path
		if err != nil {
			t.Fatal(err)
		}
		value, err := os.ReadFile(os.Getenv("NANITE_SCHEMA_VALUE")) //nolint:gosec // a test input path
		if err != nil {
			t.Fatal(err)
		}
		runCase(schema, value)
		return
	}
	t.Skip("run by the child-process tests")
}

type childResult struct {
	outcome string
	took    time.Duration
	rss     int64
}

func runChild(t *testing.T, env ...string) childResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReproChild$", "-test.count=1") //nolint:gosec // re-runs this test binary
	cmd.Env = append(append(os.Environ(), "GOMEMLIMIT=384MiB"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, tail(string(out)))
	}
	var r childResult
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "OUTCOME: "):
			r.outcome = strings.TrimPrefix(line, "OUTCOME: ")
		case strings.HasPrefix(line, "MS: "):
			ms, _ := strconv.Atoi(strings.TrimPrefix(line, "MS: "))
			r.took = time.Duration(ms) * time.Millisecond
		case strings.HasPrefix(line, "RSS: "):
			r.rss, _ = strconv.ParseInt(strings.TrimPrefix(line, "RSS: "), 10, 64)
		}
	}
	if r.outcome == "" {
		t.Fatalf("no outcome:\n%s", tail(string(out)))
	}
	return r
}

func checkChild(t *testing.T, r childResult, want []string) {
	t.Helper()
	if !contains(want, r.outcome) {
		t.Errorf("outcome %q, want one of %q", r.outcome, want)
	}
	if r.took > caseWall {
		t.Errorf("took %s, limit %s", r.took, caseWall)
	}
	if r.rss > caseRSS {
		t.Errorf("peak RSS %d MB, limit %d MB", r.rss>>20, caseRSS>>20)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestReprosRunInAChildProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	for name, tc := range reproCases() {
		t.Run(name, func(t *testing.T) {
			r := runChild(t, "NANITE_SCHEMA_REPRO="+name)
			t.Logf("%s in %s, peak RSS %d MB", r.outcome, r.took, r.rss>>20)
			checkChild(t, r, tc.want)
		})
	}
}

// TestCaseFiles runs a directory of <name>.schema.json / <name>.input.json
// pairs, each in a child process, when NANITE_SCHEMA_CASES names one; with
// NANITE_SCHEMA_REAL it also checks a directory of realistic schemas resolve
// and validate {} within budget.
func TestCaseFiles(t *testing.T) {
	dir, realDir := os.Getenv("NANITE_SCHEMA_CASES"), os.Getenv("NANITE_SCHEMA_REAL")
	if dir == "" && realDir == "" {
		t.Skip("set NANITE_SCHEMA_CASES and/or NANITE_SCHEMA_REAL")
	}
	if dir != "" {
		schemas, _ := filepath.Glob(filepath.Join(dir, "*.schema.json"))
		for _, path := range schemas {
			name := strings.TrimSuffix(filepath.Base(path), ".schema.json")
			t.Run(name, func(t *testing.T) {
				r := runChild(t, "NANITE_SCHEMA_FILE="+path, "NANITE_SCHEMA_VALUE="+filepath.Join(dir, name+".input.json"))
				t.Logf("%s in %s, peak RSS %d MB", r.outcome, r.took, r.rss>>20)
				checkChild(t, r, []string{"refused", "invalid schema", "budget", "too deep", "valid", "invalid"})
			})
		}
	}
	if realDir != "" {
		schemas, _ := filepath.Glob(filepath.Join(realDir, "*.json"))
		empty := filepath.Join(t.TempDir(), "empty.json")
		if err := os.WriteFile(empty, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, path := range schemas {
			t.Run("real/"+filepath.Base(path), func(t *testing.T) {
				r := runChild(t, "NANITE_SCHEMA_FILE="+path, "NANITE_SCHEMA_VALUE="+empty)
				t.Logf("%s in %s, peak RSS %d MB", r.outcome, r.took, r.rss>>20)
				checkChild(t, r, []string{"valid"})
			})
		}
	}
}

func tail(s string) string {
	if len(s) > 2000 {
		return "…" + s[len(s)-2000:]
	}
	return s
}

// bigRegexp compiles to about 25,000 instructions, past MaxRegexpInsts
// (and past MaxSchemaRegexpInsts / 4).
var bigRegexp = func() string {
	alts := make([]string, 25)
	for i := range alts {
		alts[i] = string(rune('a'+i)) + "{1000}"
	}
	return strings.Join(alts, "|")
}()

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
		"wide anyOf is fine":        {`{"anyOf":[` + repeat(5000, `{"type":"string"}`) + `]}`, ""},
		"too many paths":            {`{"anyOf":[` + repeat(MaxInPlacePaths+1, `{"type":"string"}`) + `]}`, "ways"},
		"regex program too large":   {`{"pattern":"` + bigRegexp + `"}`, "instructions"},
		"patterns too large together": {`{"properties":{` + func() string {
			p := make([]string, 30) // 30 x ~4,000 instructions
			for i := range p {
				p[i] = fmt.Sprintf(`"p%d":{"type":"string","pattern":"[a-z]{1000}x{1000}y{1000}z{1000}"}`, i)
			}
			return strings.Join(p, ",")
		}() + `}}`, "together"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(json.RawMessage(tc.schema))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateBudgetAndContext(t *testing.T) {
	resolved, err := Resolve(json.RawMessage(`{"allOf":[{"type":"array","items":{"$ref":"#"}},{"type":"array","items":{"$ref":"#"}}]}`))
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
	if err := Validate(t.Context(), resolved, nest(8)); err != nil {
		t.Fatalf("small value: %v", err)
	}
	if err := Validate(t.Context(), resolved, nest(30)); !errors.Is(err, jsonschema.ErrBudget) {
		t.Fatalf("2^30 visits: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	big := make([]any, 5000)
	for i := range big {
		big[i] = []any{}
	}
	if err := Validate(ctx, resolved, big); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
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
	if err := Validate(t.Context(), resolved, nest(MaxValueDepth)); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if err := Validate(t.Context(), resolved, nest(MaxValueDepth+2)); !errors.Is(err, ErrValueTooDeep) {
		t.Fatalf("past the limit: %v", err)
	}
	// Typed values are measured too.
	type deep []any
	if err := Validate(t.Context(), resolved, deep{nest(MaxValueDepth + 2)}); !errors.Is(err, ErrValueTooDeep) {
		t.Fatalf("typed slice: %v", err)
	}
	alts := make([]string, 400)
	for i := range alts {
		alts[i] = fmt.Sprintf(`{"const":"value number %d, which is long enough to add up"}`, i)
	}
	wide, err := Resolve(json.RawMessage(`{"anyOf":[` + strings.Join(alts, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	err = Validate(t.Context(), wide, "nope")
	if err == nil || len(err.Error()) > MaxValidationError+64 {
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
	resolveCache.Lock()
	defer resolveCache.Unlock()
	if resolveCache.bytes > resolveCacheBytes || resolveCache.order.Len() != len(resolveCache.entries) {
		t.Fatalf("cache %d bytes, %d/%d entries", resolveCache.bytes, resolveCache.order.Len(), len(resolveCache.entries))
	}
}
