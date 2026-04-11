# [High] `math_eval` recursive-descent parser has no depth limit → stack overflow crash

**Scope:** `internal/mcp/general_tools.go` — `evalExpr` / `mathParser`
**Topic:** Security — panic via malformed input at a trust boundary, memory & resources
**Date:** 2026-04-10

## Problem

The `math_eval` tool is a hand-written recursive-descent parser with no recursion-depth cap. Every `(` in the input triggers a recursive call to `parseExpr` → `parseAddSub` → `parseMulDiv` → `parsePower` → `parseUnary` → `parseAtom`, and `parseAtom` on `(` calls `parseExpr` again — five stack frames per paren depth. An expression of a few tens of thousands of open parens blows the goroutine stack and panics the nanite process. Similarly, `parsePower` is right-associative recursive (`base^exp^exp^...`) and also uncapped. Both are reachable via LLM-generated tool arguments.

Go goroutine stacks grow dynamically up to `runtime.SetMaxStack` (default 1GB), so the "crash" looks like either a stack-overflow fatal error that is unrecoverable (Go runtime aborts the whole process) **or** runaway memory growth before the stack cap is hit. The `recover()` does not catch `runtime.Goexit`-class fatal errors, so this is a process-kill, not a handleable panic.

## Evidence

Parser entry:

```go
// internal/mcp/general_tools.go:446-457
func evalExpr(expr string) (float64, error) {
    p := &mathParser{input: strings.TrimSpace(expr)}
    result, err := p.parseExpr()
    ...
}
```

Recursive descent through parens:

```go
// internal/mcp/general_tools.go:554-584
func (p *mathParser) parseAtom() (float64, error) {
    p.skipSpaces()
    if p.pos >= len(p.input) {
        return 0, fmt.Errorf("unexpected end of expression")
    }

    // Parenthesized expression.
    if p.input[p.pos] == '(' {
        p.pos++
        val, err := p.parseExpr()
        if err != nil {
            return 0, err
        }
        ...
    }
    ...
}
```

Right-associative power:

```go
// internal/mcp/general_tools.go:520-535
func (p *mathParser) parsePower() (float64, error) {
    base, err := p.parseUnary()
    if err != nil {
        return 0, err
    }
    p.skipSpaces()
    if p.pos < len(p.input) && p.input[p.pos] == '^' {
        p.pos++
        exp, err := p.parsePower() // right-associative
        if err != nil {
            return 0, err
        }
        return math.Pow(base, exp), nil
    }
    return base, nil
}
```

There is no counter threaded through any of the `parse*` methods. Go's goroutine stack starts at 8KB and doubles until it hits `runtime.SetMaxStack(1GB by default)`, so the attack succeeds silently until the runtime issues `fatal: runtime: goroutine stack exceeds N-byte limit` and the entire process dies. A `recover()` in the handler cannot catch this — stack-overflow is fatal per the Go runtime's design.

Also note: `parseUnary` recurses through repeated unary minus — `-----------5` adds one frame per `-`. Same class, same fix.

## Impact

- **Crash:** process termination. Every in-flight chat turn, streaming response, worker, PTY session, plugin hook, and cached goroutine goes away with the process. Users see nanite exit; any server mode requires a restart.
- **Reliability:** the tool is enabled by default in every chat session, so a malicious `math_eval("(((...(1)...)))")` from a prompt-injection payload is a one-call liveness attack.
- **Reproducibility:** deterministic. Any expression with enough nested parens (~40k-100k on a default 1GB stack limit) will trip it.
- **Severity:** High, not Critical, because it's a crash primitive rather than a security bypass. It becomes a reliability blocker the first time a user runs nanite against an adversarial transcript.

Reproduction:

```go
// Inside a test
strings.Repeat("(", 100_000) + "1" + strings.Repeat(")", 100_000)
```

This will either panic with stack overflow or consume hundreds of MB of stack space before unwinding. Either outcome is unacceptable in a default-on tool.

## Recommendation

Two options, either is sufficient:

**Option A — depth limit.** Thread a `depth` counter through the parser and abort past a small constant:

```go
const maxMathDepth = 64

type mathParser struct {
    input string
    pos   int
    depth int
}

func (p *mathParser) parseExpr() (float64, error) {
    p.depth++
    if p.depth > maxMathDepth {
        return 0, fmt.Errorf("expression too deeply nested")
    }
    defer func() { p.depth-- }()
    return p.parseAddSub()
}
```

Apply to `parseExpr`, `parseAtom` (the paren branch), `parsePower` (the right-associative recursion), and `parseUnary`.

**Option B — delete the hand-rolled parser.** The `go/parser` package in the stdlib can parse arithmetic via `parser.ParseExpr`, and a small visitor over `ast.BinaryExpr` / `ast.BasicLit` / `ast.ParenExpr` evaluates safely with the Go runtime's own recursion limits (which are much higher than any reasonable input, and return proper errors rather than crashes). Or use one of the many battle-tested expression libraries (`expr-lang/expr`). Removing hand-written parsers from the trust boundary is a more durable fix than auditing one's own parser.

**Recommended: Option A as the immediate fix, Option B as the follow-up simplification.** The depth limit unblocks this finding in a handful of lines. Replacing the parser with a library is the right long-term answer but is more invasive.

**Related smaller fix.** While in the file, add a length cap on `expr` itself — `if len(expr) > 1024 { error }`. No legitimate arithmetic input needs more than a few hundred characters, and this stops the length-based OOM variant before it reaches the parser.

## References

- `internal/mcp/general_tools.go:L446-L457` — `evalExpr` entrypoint
- `internal/mcp/general_tools.go:L554-L584` — `parseAtom` with unchecked recursion
- `internal/mcp/general_tools.go:L520-L535` — right-associative `parsePower`
- `internal/mcp/general_tools.go:L537-L552` — `parseUnary` recursion
- Go runtime: `runtime.SetMaxStack`, fatal stack-overflow semantics
- CWE-674 — Uncontrolled Recursion
- Go stdlib: `go/parser.ParseExpr` as a safer alternative
