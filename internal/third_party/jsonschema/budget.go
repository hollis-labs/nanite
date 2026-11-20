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
	"regexp"
	"regexp/syntax"
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
	// ctxEvery is how many visits pass between context checks.
	ctxEvery = 1024
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
// bytes, and nothing built once the error text budget is spent.
func (st *state) errorf(format string, args ...any) error {
	if st.abort != nil {
		return st.abort
	}
	if st.ctx != nil && st.errBytesLeft <= 0 {
		return errOmitted
	}
	for i, a := range args {
		if _, isErr := a.(error); !isErr {
			args[i] = bounded{a}
		}
	}
	err := fmt.Errorf(format, args...)
	st.spendText(len(err.Error()))
	return err
}

// wrap prefixes *errp with the schema it came from, as upstream's wrapf did,
// unless the error text budget is spent or validation was aborted.
func (st *state) wrap(errp *error, schema *Schema) {
	if *errp == nil {
		return
	}
	if st.abort != nil {
		*errp = st.abort
		return
	}
	if st.ctx != nil && st.errBytesLeft <= 0 {
		*errp = errOmitted
		return
	}
	*errp = fmt.Errorf("validating %s: %w", st.rs.schemaString(schema), *errp)
	st.spendText(len((*errp).Error()))
}

func (st *state) spendText(n int) {
	if st.ctx != nil {
		st.errBytesLeft -= n
	}
}

// joined is errors.Join that builds nothing once the error text budget is spent.
func (st *state) joined(errs []error) error {
	if st.ctx != nil && st.errBytesLeft <= 0 {
		return errOmitted
	}
	err := errors.Join(errs...)
	st.spendText(len(err.Error()))
	return err
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
