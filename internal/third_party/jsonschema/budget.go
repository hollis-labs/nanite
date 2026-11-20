// Copyright 2025 The JSON Schema Go Project Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Nanite patch (not upstream): a context and a work budget for Validate, a
// bound on the error text it builds, a bound on regular expression programs,
// and an accessor for resolved reference targets. See README.md.

package jsonschema

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
)

// Budget bounds the work of one Validate call.
type Budget struct {
	// Visits is the most units of work Validate may do: one per schema
	// applied to a value, one per enum entry or array item compared, and,
	// for a regular expression, the input length times the program size in
	// units of 256 instructions. Zero means DefaultVisits.
	Visits int
	// ErrorBytes is the most error text Validate builds. Past it, further
	// failures are reported without detail. Zero means DefaultErrorBytes.
	ErrorBytes int
}

const (
	// DefaultVisits is the visit budget when Budget.Visits is zero.
	DefaultVisits = 200_000
	// DefaultErrorBytes is the error text budget when Budget.ErrorBytes is zero.
	DefaultErrorBytes = 16 << 10
	// MaxRegexpInsts bounds a compiled pattern's program, checked when the
	// schema is resolved.
	MaxRegexpInsts = 20_000
	// maxShownValue bounds how much of one value an error message shows.
	maxShownValue = 256
	// ctxEvery is how many units pass between context checks. Every unit is
	// bounded work (a node, or 64 bytes of a string), so this is well under a
	// millisecond.
	ctxEvery = 64
)

// ErrBudget is returned (wrapped) when validation exceeds its Budget.
var ErrBudget = errors.New("jsonschema: validation budget exceeded")

// errOmitted stands in for failures once the error text budget is spent.
var errOmitted = errors.New("(further detail omitted)")

func (st *state) init(ctx context.Context, b Budget) {
	st.ctx = ctx
	st.visitsLeft = b.Visits
	if st.visitsLeft <= 0 {
		st.visitsLeft = DefaultVisits
	}
	st.errBytesLeft = b.ErrorBytes
	if st.errBytesLeft <= 0 {
		st.errBytesLeft = DefaultErrorBytes
	}
	st.sinceCtx = ctxEvery // the first charge checks the context
}

// charge spends n units of the budget. Once the budget is spent, or the
// context is done, it records why in st.abort and every later call fails
// at once, so the rest of the traversal unwinds without doing work.
func (st *state) charge(n int) error {
	if st.abort != nil {
		return st.abort
	}
	if st.ctx == nil { // validateDefaults and ApplyDefaults run unbudgeted
		return nil
	}
	st.visitsLeft -= n
	st.sinceCtx += n
	if st.visitsLeft < 0 {
		st.abort = ErrBudget
		return st.abort
	}
	if st.sinceCtx >= ctxEvery {
		st.sinceCtx = 0
		if err := st.ctx.Err(); err != nil {
			st.abort = fmt.Errorf("jsonschema: validation stopped: %w", err)
			return st.abort
		}
	}
	return nil
}

// errorf is fmt.Errorf with every argument shown in at most maxShownValue
// bytes. Once the error text budget is spent, it builds nothing.
func (st *state) errorf(format string, args ...any) error {
	if st.abort != nil {
		return st.abort
	}
	if st.textSpent() {
		return errOmitted
	}
	format = typeVerbs(format, args)
	for i, a := range args {
		if _, isErr := a.(error); !isErr {
			// fmt renders the whole value before the bound cuts it, so the
			// work is charged by the value's size. This is never refunded,
			// even when the text is discarded.
			v, ok := a.(reflect.Value)
			if !ok {
				v = reflect.ValueOf(a)
			}
			if err := st.chargeSize(v); err != nil {
				return err
			}
			args[i] = bounded{a}
		}
	}
	err := fmt.Errorf(format, args...)
	st.spendText(len(err.Error()))
	return err
}

// wrap prefixes *errp with the schema it came from, as upstream's wrapf did.
// The prefix is kept apart from the inner error and joined only when Error
// is called, so wrapping is charged (and allocates) only the bytes it adds.
// Once the error text budget is spent, the inner error is kept as it is.
func (st *state) wrap(errp *error, schema *Schema) {
	if *errp == nil {
		return
	}
	if st.abort != nil {
		*errp = st.abort
		return
	}
	if st.textSpent() || *errp == errOmitted { //nolint:errorlint // the sentinel itself, never wrapped
		return
	}
	prefix := "validating " + st.rs.schemaString(schema)
	*errp = &wrappedError{prefix: prefix, err: *errp}
	st.spendText(len(prefix) + 2)
}

// wrappedError is "prefix: err", built when Error is called.
type wrappedError struct {
	prefix string
	err    error
}

func (e *wrappedError) Error() string { return e.prefix + ": " + e.err.Error() }
func (e *wrappedError) Unwrap() error { return e.err }

func (st *state) textSpent() bool { return st.ctx != nil && (st.errBytesLeft <= 0 || st.quiet > 0) }

func (st *state) spendText(n int) {
	if st.ctx != nil {
		st.errBytesLeft -= n
	}
}

// joined is errors.Join of the errors that still carry detail; the text is
// built only when Error is called.
func (st *state) joined(errs []error) error {
	kept := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != errOmitted { //nolint:errorlint // the sentinel itself, never wrapped
			kept = append(kept, err)
		}
	}
	if len(kept) == 0 {
		return errOmitted
	}
	st.spendText(len(kept))
	return errors.Join(kept...)
}

// equal is equalValue that charges the budget for each node it compares and
// stops at the first difference.
func (st *state) equal(x, y reflect.Value) (bool, error) {
	if err := st.charge(1); err != nil {
		return false, err
	}
	if !x.IsValid() || !y.IsValid() {
		return x.IsValid() == y.IsValid(), nil
	}
	for x.Kind() == reflect.Interface && !x.IsNil() {
		x = x.Elem()
	}
	for y.Kind() == reflect.Interface && !y.IsNil() {
		y = y.Elem()
	}
	switch {
	case x.Kind() == reflect.String && y.Kind() == reflect.String:
		if err := st.charge(min(x.Len(), y.Len()) / 64); err != nil {
			return false, err
		}
		return x.String() == y.String(), nil
	case (x.Kind() == reflect.Slice || x.Kind() == reflect.Array) && x.Kind() == y.Kind():
		if x.Len() != y.Len() {
			return false, nil
		}
		for i := range x.Len() {
			if ok, err := st.equal(x.Index(i), y.Index(i)); !ok || err != nil {
				return false, err
			}
		}
		return true, nil
	case x.Kind() == reflect.Map && y.Kind() == reflect.Map:
		if x.Len() != y.Len() || x.IsNil() != y.IsNil() {
			return false, nil
		}
		for it := x.MapRange(); it.Next(); {
			vy := y.MapIndex(it.Key())
			if !vy.IsValid() {
				return false, nil
			}
			if ok, err := st.equal(it.Value(), vy); !ok || err != nil {
				return false, err
			}
		}
		return true, nil
	}
	// Numbers, booleans, nil and the rest are compared as upstream does.
	return equalValue(x, y), nil
}

// chargeSize charges the budget for walking v (one unit per node, and one
// per 64 bytes of a string), stopping as soon as the budget is spent.
func (st *state) chargeSize(v reflect.Value) error {
	if err := st.charge(1); err != nil {
		return err
	}
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() { //nolint:exhaustive // only strings and containers cost more than a node
	case reflect.String:
		return st.charge(v.Len() / 64)
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := st.chargeSize(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			if err := st.chargeSize(it.Key()); err != nil {
				return err
			}
			if err := st.chargeSize(it.Value()); err != nil {
				return err
			}
		}
	}
	return nil
}

// typeVerbs replaces each %T verb in format with the type name of the
// argument it refers to (as text), because fmt prints %T itself and would
// otherwise show the bounded wrapper's type. Only the plain forms are used
// here: %T and %[n]T.
func typeVerbs(format string, args []any) string {
	if !strings.Contains(format, "T") {
		return format
	}
	var b strings.Builder
	next := 0 // the argument the next sequential verb uses
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		j := i + 1
		if j < len(format) && format[j] == '%' {
			b.WriteString("%%")
			i = j
			continue
		}
		arg := next
		if j < len(format) && format[j] == '[' {
			end := strings.IndexByte(format[j:], ']')
			if end < 0 {
				b.WriteString(format[i:])
				return b.String()
			}
			n, err := strconv.Atoi(format[j+1 : j+end])
			if err != nil {
				b.WriteString(format[i:])
				return b.String()
			}
			arg = n - 1
			j += end + 1
		}
		// Flags, width and precision.
		k := j
		for k < len(format) && strings.IndexByte("+-# 0123456789.", format[k]) >= 0 {
			k++
		}
		if k >= len(format) {
			b.WriteString(format[i:])
			return b.String()
		}
		if format[k] == 'T' && arg >= 0 && arg < len(args) {
			b.WriteString(strings.ReplaceAll(fmt.Sprintf("%T", args[arg]), "%", "%%"))
		} else {
			// Index every other verb explicitly: a %T taken out no longer
			// consumes its argument.
			b.WriteString("%[" + strconv.Itoa(arg+1) + "]" + format[j:k+1])
		}
		next = arg + 1
		i = k
	}
	return b.String()
}

// bounded formats its value as fmt would, but writes at most maxShownValue
// bytes of it.
type bounded struct{ v any }

func (b bounded) Format(f fmt.State, verb rune) {
	w := &limitWriter{w: f, left: maxShownValue}
	fmt.Fprintf(w, fmt.FormatString(f, verb), b.v)
	if w.cut {
		_, _ = io.WriteString(f, "…")
	}
}

type limitWriter struct {
	w    io.Writer
	left int
	cut  bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	n := len(p)
	if l.left <= 0 {
		l.cut = true
		return n, nil
	}
	if len(p) > l.left {
		p, l.cut = p[:l.left], true
	}
	l.left -= len(p)
	_, err := l.w.Write(p)
	return n, err
}

// compileRegexp compiles a schema pattern, refusing one whose program is
// larger than MaxRegexpInsts. It also returns the program size, in units of
// 256 instructions (at least 1), which matching charges per input byte.
func compileRegexp(pattern string) (*regexp.Regexp, int, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, 0, err
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, 0, err
	}
	prog, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		return nil, 0, err
	}
	if n := len(prog.Inst); n > MaxRegexpInsts {
		return nil, 0, fmt.Errorf("regular expression program has %d instructions; the limit is %d", n, MaxRegexpInsts)
	}
	return re, 1 + len(prog.Inst)/256, nil
}

// match runs re on s, charging the budget for it first.
func (st *state) match(info *resolvedInfo, re *regexp.Regexp, s string) (bool, error) {
	if err := st.charge(max(1, len(s)) * max(1, info.regexpCost[re])); err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

// RefTargets reports what s's $ref and $dynamicRef resolved to: ref and dyn
// are the lexical targets (nil if none), and anchor is set when the
// $dynamicRef resolves at validation time to the outermost schema in scope
// with that $dynamicAnchor. s must belong to rs.
func (rs *Resolved) RefTargets(s *Schema) (ref, dyn *Schema, anchor string) {
	info := rs.resolvedInfos[s]
	if info == nil {
		return nil, nil, ""
	}
	return info.resolvedRef, info.resolvedDynamicRef, info.dynamicRefAnchor
}

// smallerSide iterates the entries of m (a keyword keyed by property name)
// that the instance has, from whichever side is smaller: m's keys, or a map
// instance's keys. Visiting a key the instance lacks finds nothing, so the
// result is the same either way.
func smallerSide[T any](m map[string]T, instance reflect.Value) iter.Seq2[string, T] {
	return func(yield func(string, T) bool) {
		if instance.Kind() == reflect.Map && instance.Len() < len(m) && instance.Type().Key().Kind() == reflect.String {
			for it := instance.MapRange(); it.Next(); {
				prop := it.Key().String()
				if v, ok := m[prop]; ok && !yield(prop, v) {
					return
				}
			}
			return
		}
		for prop, v := range m {
			if !yield(prop, v) {
				return
			}
		}
	}
}

// chargeKeys charges for visiting the smaller of a keyword's keys and a map
// instance's keys, as smallerSide does.
func (st *state) chargeKeys(n int, instance reflect.Value) error {
	if instance.Kind() == reflect.Map {
		n = min(n, instance.Len())
	}
	return st.charge(n)
}
