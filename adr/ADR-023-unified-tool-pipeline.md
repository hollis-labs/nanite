# ADR-023: Unified Tool Pipeline — Selection, Gating, and Filtering in ToolBroker

**Status:** Accepted
**Date:** 2026-03-14
**Deciders:** chrispian, Mentat

## Context

Fragments Engine has three distinct tool control layers scattered across different systems:

1. **Selection** (pre-call) — Which tools can the agent see? Handled by ToolBroker (`core/broker`), a Go library with intent-based rule matching.
2. **Gating** (pre-execution) — Is this specific call/command allowed? Handled by shell scripts (`envelope-guard.sh`) and Hadron's `ValidateCommand()` with hardcoded substring matching.
3. **Filtering** (post-execution) — What does the agent get back? Handled by tokf (external Rust CLI) and Mentat's `internal/filter/` (Go, emoji stripping).

This fragmentation causes real problems:
- **CC#15897**: Multiple PreToolUse hooks matching the same tool silently discard `updatedInput`. Our envelope-guard (matching Bash) killed tokf's command rewrite for 3+ days before discovery.
- **Hadron's exec guards** use brittle substring matching (`strings.Contains`) with a hardcoded deny list. No glob patterns, no per-blueprint overrides, no configurability.
- **No unified audit trail** — gate decisions are logged in shell script output (lost), not structured or observable.
- **No output filtering in Hadron** — blueprint execution streams raw PTY output with zero transformation.

ToolBroker already has the right abstractions: rule engine, priority ordering, intent matching, glob patterns, tag-based matching. It just needs to be extended from tool selection to the full pipeline.

## Decision

### Extend ToolBroker with three typed layers

Add a `Layer` field to the existing `Rule` struct. Rules are categorized as `select`, `gate`, or `filter`. One rule engine, one YAML format, one mental model.

### Rule Format (Approach C: Hybrid Typed)

```yaml
rules:
  # Layer 1: Tool Selection (existing capability)
  - name: hide-blueprint-runners
    layer: select
    intent: "*"
    match:
      patterns: ["hadron_bp_*"]
    action:
      type: exclude
    priority: 50

  # Layer 2: Execution Gating (new)
  - name: block-dangerous
    layer: gate
    match:
      commands: ["rm -rf /", "dd if=*", "mkfs.*", "sudo *"]
    action:
      type: deny
      reason: "Dangerous command blocked by gate policy"
    priority: 100

  - name: protect-state-paths
    layer: gate
    match:
      write_paths: [".agentrc/pcc/global/*", ".agentrc/state/*"]
    action:
      type: deny
      reason: "Protected path — write through MCP tools"
    priority: 90

  - name: confirm-destructive
    layer: gate
    match:
      commands: ["git push --force*", "git reset --hard*"]
    action:
      type: ask
      reason: "Destructive — confirm before proceeding"
    priority: 80

  - name: go-toolchain-allowed
    layer: gate
    match:
      commands: ["go *"]
    action:
      type: allow
    priority: 50

  # Layer 3: Output Filtering (new)
  - name: tokf-compression
    layer: filter
    match:
      commands: ["go *", "git *", "npm *", "ls *"]
    action:
      type: transform
      engine: tokf
    priority: 50

  - name: pii-redaction
    layer: filter
    match:
      commands: ["*"]
    action:
      type: transform
      engine: gitleaks
      config: ".agentrc/gitleaks.toml"
    priority: 90
```

### Go Type Extensions

```go
type Rule struct {
    Name     string `yaml:"name"`
    Layer    string `yaml:"layer"`     // "select", "gate", "filter"
    Intent   string `yaml:"intent,omitempty"`
    Match    Match  `yaml:"match"`
    Action   Action `yaml:"action"`
    Priority int    `yaml:"priority,omitempty"`
}

type Match struct {
    // Tool selection (existing)
    Patterns []string `yaml:"patterns,omitempty"`
    Tags     []string `yaml:"tags,omitempty"`
    Servers  []string `yaml:"servers,omitempty"`
    // Command gating (new)
    Commands   []string `yaml:"commands,omitempty"`
    WritePaths []string `yaml:"write_paths,omitempty"`
}

type Action struct {
    Type   string `yaml:"type"`                  // include, exclude, summarize, allow, deny, ask, transform
    Reason string `yaml:"reason,omitempty"`       // for deny/ask
    Engine string `yaml:"engine,omitempty"`       // for transform: "tokf", "gitleaks"
    Config string `yaml:"config,omitempty"`       // engine-specific config path
}
```

### Three Entry Points, One Codebase

| Entry Point | Consumer | How |
|-------------|----------|-----|
| **Go library** | Hadron, Mentat | `import "core/broker"` — direct function calls |
| **CLI binary** | Claude Code hooks (via thin shell adapter) | `toolbroker gate --tool Bash --input ...` |
| **Shell mode** | Hadron controlled execution | `SHELL=toolbroker` — wraps command exec |

### Claude Code Integration

Single thin shell script per hook event, routing to the Go binary:

```
PreToolUse:*  → fe-gate.sh → toolbroker gate --config .agentrc/broker-rules.yaml
PostToolUse:* → fe-filter.sh → toolbroker filter --config .agentrc/broker-rules.yaml
```

One hook per event = CC#15897 immunity. All logic lives in the Go binary.

### Hadron Integration

Hadron imports `core/broker` directly (no CLI overhead). Replaces `ValidateCommand()` substring matching with the same rule engine:

```go
rules, _ := broker.LoadRulesFromFile(".hadron/gate-rules.yaml")
lb := broker.NewLocalBroker(nil, rules)
allowed, reason := lb.GateCommand(ctx, cmd, opts)
```

Hadron keeps working without AI — gate rules are pure config. Per-blueprint rule overrides allow some blueprints to use `rm -rf` while others can't.

### Output Filtering Strategy

- **tokf** for MVP output compression (delegate via `tokf run`)
- **gitleaks** for secrets/PII detection (delegate via `gitleaks detect --pipe`)
- **Mentat internal filters** stay separate — they handle LLM response content (emoji, formatting), not CLI output. See Section 3 of `docs/tooling/tokf-research-and-integration-plan.md`.
- Future: fork tokf's TOML filter format into a Go-native implementation if needed

### Audit & Observability

Every gate decision emits a structured log entry and optional OTel span:
- Tool name, command, decision (allow/deny/ask), reason, rule name, timing
- Feeds into `core/otel` pipeline for portfolio-wide cost/security dashboards
- Replaces lost shell script output with queryable, structured data

## Consequences

### Positive
- One rule engine for all three control layers — type-safe, testable, configurable
- CC#15897 workaround becomes permanent architecture (one hook per event)
- Hadron exec guards upgraded from substring matching to glob patterns + priority rules
- Per-blueprint, per-workspace, per-agent rule overrides
- Unified audit trail with OTel integration
- Same library works standalone in Hadron (no FE service dependencies)
- Shell scripts remain as thin adapters — easy to understand, easy to replace

### Negative
- Build dependency: CLI binary must be compiled before Claude Code hooks work
- tokf becomes an external dependency (called via subprocess)
- Migration effort for Hadron's existing `ValidateCommand()` + `ExecutionSettings`
- Rule file per project adds config surface area

### Risks
- Over-broad deny rules blocking legitimate agent work — start with known-dangerous only, expand gradually
- tokf subprocess overhead on every filtered command — measure, optimize if > 100ms
- Rule priority conflicts — need clear documentation of evaluation order

## Related

- ADR-021: Automated Quality Gates (complementary — quality gates + tool pipeline = defense in depth)
- ADR-020: Hadron Blueprint Skill Surfacing (ToolBroker selection layer)
- `docs/tooling/tokf-research-and-integration-plan.md`: tokf research, CC#15897 documentation
- `docs/architecture/tool-broker-design.md`: Original ToolBroker vision (Phase 1-4)
- EPIC-20260314-51504: Core Library Consolidation (where `tool-broker` moves to `core/broker`)
