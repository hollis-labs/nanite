# jsonschema (vendored)

This is `github.com/google/jsonschema-go/jsonschema` at **v0.4.3**, copied
into Nanite with a small patch. The upstream copyright and MIT license are
in `LICENSE`, and every upstream file keeps its header.

Copied from upstream:
- the package source;
- the tests `validate_test.go`, `draft07_test.go`, `resolve_test.go`,
  `schema_test.go` and `json_pointer_test.go`;
- `testdata/`, including the JSON-Schema-Test-Suite subset upstream runs;
- `meta-schemas/`.

`infer_test.go` and `util_test.go` are left out: they declare struct fields
that `go vet` rejects by design, and they do not exercise validation.

Upstream files are byte-identical to v0.4.3, except:
- `validate.go` and `resolve.go`, which carry the patch call sites (marked
  "Nanite patch");
- `validate_test.go` and `draft07_test.go`, where `Validate(x)` became
  `Validate(context.Background(), x, Budget{})`;
- `validate.go` (one comment) and `testdata/draft2020-12/additionalProperties.json`
  (six "core" specification references to section 10.3.2, item 3, metadata the tests
  do not read): spec section numbers written out differently, because the
  public-repo scan reads `n.n.n.n` as an IP address and an allowlist entry
  still fails it.

The repository's `.golangci.yml` excludes third_party files from linting,
except files named `budget*`, so the patch itself is linted.

## Why it is vendored

Upstream validates by plain recursion and explores every alternative of
`anyOf`, `allOf`, `oneOf` and the like, with no bound on the work. Some
schema and value pairs make it run for minutes, exhaust memory, or overflow
the stack, which is a fatal runtime error that no `recover` catches. Nanite
validates schemas and values it does not control: tool schemas from MCP
servers and hosts, and values from a model. So a single tool or tool call
could end the whole process. Static limits on the schema cannot rule this
out, because the work multiplies across the levels of the value: a 97-byte
schema was enough. The validator itself has to be bounded.

## The patch

Most of it is in `budget.go`. The call sites in `validate.go` and
`resolve.go` are marked "Nanite patch".

**Context and budget.** `Resolved.Validate(ctx, instance, Budget)` takes a
context and a work budget.
- It stops with `ErrBudget` past `Budget.Visits` units (default 200,000).
- It stops with the context's error, which is checked on the first unit and
  every 64 units after that, well under a millisecond apart.

**What a unit charges.** Work is charged in proportion to what it does:
- One unit per schema applied to a value.
- `enum` and `const`: one unit per node compared, through an equality walk
  that stops at the first difference.
- `uniqueItems`: each item's size (nodes, plus 1 per 64 bytes of a string).
- `minLength` and `maxLength`: 1 per 64 bytes of the string.
- `required` and `dependentRequired`: the names checked.
- A falsy `additionalProperties`: the instance's keys.
- `properties` visits the smaller side, schema properties or instance keys,
  and is charged for it.
- A regular-expression match: the input length times the program size, in
  units of 256 instructions.

**Error text.**
- Each value or schema shown in a message is cut to 256 bytes.
- `%T` verbs are rendered before arguments are wrapped.
- A wrap adds its "validating …" prefix lazily and is charged only for the
  bytes it adds.
- Failures whose text would be discarded build none. These are the branches
  of `oneOf`, `not`, `if` and the items tried by `contains`.
- When `anyOf` passes, the text its failed branches cost is refunded.
- Past `Budget.ErrorBytes` (default 16KB), new failures carry no detail, but
  errors already built are kept as they are. So a deep failure still names
  its leaf path and what was wrong.

**Patterns.** A pattern whose compiled program exceeds 20,000 instructions
is refused when the schema is resolved.

**Reference targets.** `Resolved.RefTargets(s)` reports what a schema's
`$ref` and `$dynamicRef` resolved to, so callers can analyse the reference
graph without reflection.

`ValidateDefaults` and `ApplyDefaults` keep upstream behaviour (unbudgeted).

## Upgrading

1. Copy the new upstream files over these.
2. Keep `budget.go` and `budget_test.go`.
3. Re-apply the marked call sites and the test adaptations.
4. Run the upstream tests here and Nanite's tests in `internal/elicitation`,
   including the child-process repro cases. They must pass.

If upstream adopts a context and budget for `Validate`, drop this copy and
depend on upstream again.
