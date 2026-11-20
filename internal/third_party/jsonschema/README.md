# jsonschema (vendored)

This is `github.com/google/jsonschema-go/jsonschema` at **v0.4.3**, copied
into Nanite with a small patch. The upstream copyright and MIT license are
in `LICENSE`; every upstream file keeps its header. The test files, test data
and meta-schemas are not copied.

## Why it is vendored

Upstream validates by plain recursion and explores every alternative of
`anyOf`, `allOf`, `oneOf` and the like, with no bound on the work. Some
schema and value pairs make it run for minutes, exhaust memory, or overflow
the stack, which is a fatal runtime error that no `recover` catches. Nanite
validates schemas and values it does not control (tool schemas from MCP
servers and hosts, values from a model), so a single tool or tool call could
end the whole process. Static limits on the schema cannot rule this out,
because the work multiplies across the levels of the value: a 97-byte schema
was enough. The validator itself has to be bounded.

## The patch

All of it is in `budget.go`, plus the call sites it needs in `validate.go`
and `resolve.go` (marked "Nanite patch"). In addition, every `fmt.Errorf` in
a `state` method of `validate.go` became `st.errorf`, and one spec URL in a
comment is written out as a section reference, because the public-repo scan
reads its anchor (a section number) as an IP address.

- `Resolved.Validate(ctx, instance, Budget)` takes a context and a budget.
  It stops with an error wrapping `ErrBudget` past `Budget.Visits` units of
  work (default 200,000), or with the context's error, checked every 1,024
  units.
- A unit is one schema applied to a value, one `enum` entry compared, or one
  array item hashed for `uniqueItems`. A regular expression match costs the
  input length times its program size in units of 256 instructions.
- Error text is bounded: each value or schema shown in a message is cut to
  256 bytes, and once `Budget.ErrorBytes` of text has been built (default
  16KB) further failures are reported without detail, so memory stays
  bounded however many branches fail.
- A pattern whose compiled program exceeds 20,000 instructions is refused
  when the schema is resolved.
- `Resolved.RefTargets(s)` reports what a schema's `$ref` and `$dynamicRef`
  resolved to, so callers can analyse the reference graph without
  reflection.

`ValidateDefaults` and `ApplyDefaults` keep upstream behaviour (unbudgeted).

## Upgrading

Copy the new upstream files over these, keep `budget.go`, and re-apply the
marked call sites. Nanite's tests in `internal/elicitation` (including the
child-process repro cases) must pass. If upstream adopts a context and budget
for `Validate`, drop this copy and depend on upstream again.
