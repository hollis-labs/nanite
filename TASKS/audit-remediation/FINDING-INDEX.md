# Finding → Task Index

Generated from `docs/audits/2026-08-21-go-quality/findings.json` (113 findings) and
cross-validated programmatically: every finding ID below maps to exactly one task file,
and every task file below traces back to at least one real finding (the two
`quality-ratchet` standards-doc tasks are guide-derived, not finding-derived, and are
marked as such). No finding was dropped or silently absorbed into a grouping.

This is **not** the disposition table the remediation guide's §7 output format C asks
for (`remediate | already-resolved | accepted-risk | false-positive | superseded | defer |
retire | needs-architect-decision | needs-more-evidence`). That table is the `disposition`
field in `findings.json`, and filling it in is the job of task
**`00-revalidate-baseline/01-revalidate-findings-against-head.md`** — the batch's Wave 0
gate, which nothing else dispatches ahead of. Every finding here still defaults to
`remediate`, which is a placeholder inherited from the task-creation pass, not a judgment
anyone made; some findings may already be fixed by the 40 commits that landed between the
audited commit `8feeee5c` and current HEAD.

**Sequencing was added on 2026-08-21** by a planning pass. Wave assignment, cross-folder
dependencies, parallelization, and architect-decision gating live in `README.md`
(authoritative) and are mirrored into each task file's own "Planner sequencing" block.
The decision queue is `ARCHITECT-DECISIONS.md`; the guide's output-format D
(prevention/rules table) is `PREVENTION.md`.

**Two task files map to no finding by design:** `06/04`
(`04-cancellation-safety-for-terminal-writes.md`) is a fix task for `06/03`,
created 2026-08-22 for a regression the sweep exposed rather than for an audit
finding — the same fix-as-new-task convention this project uses for review
findings. `12/02` is guide-derived. Neither is an unmapped-finding error.

| Finding | Severity | Task file |
|---|---|---|
| `GO-PLUGIN-001` | critical | `01-plugin-install-convergence/01-unify-plugin-catalog-install-pipeline.md` |
| `GO-PLUGIN-002` | critical | `01-plugin-install-convergence/01-unify-plugin-catalog-install-pipeline.md` |
| `GO-SEC4-001` | critical | `02-linux-sandbox-fail-open/01-sandbox-fail-closed-without-bwrap.md` |
| `GO-AGENT-001` | high | `03-agent-slug-traversal/01-canonical-slug-path-validation.md` |
| `GO-AGENT-002` | high | `03-agent-slug-traversal/01-canonical-slug-path-validation.md` |
| `GO-PLUGIN-003` | high | `01-plugin-install-convergence/01-unify-plugin-catalog-install-pipeline.md` |
| `GO-RUNTIME-002` | high | `08-remaining-security-hardening/07-server-auth-bind-tls-posture.md` |
| `GO-SEC4-002` | high | `02-linux-sandbox-fail-open/01-sandbox-fail-closed-without-bwrap.md` |
| `GO-STORE-003` | high | `06-store-correctness/01-fix-deleteagentbyid-error-swallowing.md` |
| `GO-SVCCORE-004` | high | `08-remaining-security-hardening/01-a2a-webhook-url-validation.md` |
| `GO-SVCEXEC-001` | high | `10-architectural-concentration/01-chatserviceimpl-generateresponse-decomposition.md` |
| `GO-SVCEXEC-002` | high | `10-architectural-concentration/01-chatserviceimpl-generateresponse-decomposition.md` |
| `GO-API-004` | medium | `08-remaining-security-hardening/10-api-validation-duplication-and-pagination-bug.md` |
| `GO-API-005` | medium | `08-remaining-security-hardening/10-api-validation-duplication-and-pagination-bug.md` |
| `GO-API-007` | medium | `11-semantic-duplication-migration-drift/02-harness-v1-vs-native-durable-agent-handlers.md` |
| `GO-CHAT-002` | medium | `11-semantic-duplication-migration-drift/03-structuredmessage-unwrap-duplication.md` |
| `GO-DEP-002` | medium | `10-architectural-concentration/03-container-and-store-review-note.md` |
| `GO-EXEC-001` | medium | `05-subagent-execution-ordering/01-fix-approve-concurrency-cap-and-queued-cancel.md` |
| `GO-EXEC-002` | medium | `05-subagent-execution-ordering/01-fix-approve-concurrency-cap-and-queued-cancel.md` |
| `GO-INFRA-004` | medium | `11-semantic-duplication-migration-drift/06-provider-streaming-error-handling-divergence.md` |
| `GO-LIFE-001` | medium | `04-container-reaper-lifecycle/01-fix-container-constructor-partial-failure-cleanup.md` |
| `GO-MCPTOOL-001` | medium | `09-production-islands/04-tool-builder-yaml-architecture.md` |
| `GO-MCPTOOL-002` | medium | `09-production-islands/05-reasoning-augmented-tool-selection.md` |
| `GO-MCPTOOL-006` | medium | `10-architectural-concentration/02-selftoolstransport-decomposition.md` |
| `GO-MCPTOOL-008` | medium | `08-remaining-security-hardening/02-mcp-dev-grep-symlink-toctou.md` |
| `GO-MEM-001` | medium | `09-production-islands/01-grounding-memory-recall.md` |
| `GO-PLUGIN-004` | medium | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-RUNTIME-003` | medium | `07-runtime-correctness-lifecycle/01-fix-worktree-orphan-branch-cleanup.md` |
| `GO-RUNTIME-004` | medium | `07-runtime-correctness-lifecycle/03-bound-background-job-registry-growth.md` |
| `GO-RUNTIME-007` | medium | `07-runtime-correctness-lifecycle/05-fix-mcp-config-silent-decode-errors.md` |
| `GO-SEC-001` | medium | `08-remaining-security-hardening/08-dependency-toolchain-vuln-bumps.md` |
| `GO-SEC-003` | medium | `08-remaining-security-hardening/03-triage-remaining-gosec-g304-sites.md` |
| `GO-SEC4-003` | medium | `08-remaining-security-hardening/04-permission-default-mode-write-gap.md` |
| `GO-SEC4-004` | medium | `08-remaining-security-hardening/05-secret-key-heuristic-hardening.md` |
| `GO-SEC4-007` | medium | `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md` |
| `GO-STORE-001` | medium | `10-architectural-concentration/03-container-and-store-review-note.md` |
| `GO-STORE-005` | medium | `06-store-correctness/02-triage-store-context-and-transaction-gaps.md` |
| `GO-SVCCORE-001` | medium | `04-container-reaper-lifecycle/04-track-untracked-goroutine-spawns.md` |
| `GO-SVCCORE-003` | medium | `13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md` |
| `GO-SVCCORE-006` | medium | `04-container-reaper-lifecycle/03-investigate-internal-service-race-timeout.md` |
| `GO-SVCEXEC-003` | medium | `09-production-islands/03-team-semantic-routing.md` |
| `GO-SVCEXEC-004` | medium | `11-semantic-duplication-migration-drift/01-subagent-completion-vs-message-wake-policy.md` |
| `GO-SVCEXEC-005` | medium | `04-container-reaper-lifecycle/05-close-untested-service-config-functions.md` |
| `GO-TEST-001` | medium | `04-container-reaper-lifecycle/02-fix-api-test-container-shutdown-leak.md` |
| `GO-AGENT-003` | low | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-AGENT-004` | low | `13-mechanical-cleanup/03-naming-and-formatting-fixes.md` |
| `GO-AGENT-005` | low | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-API-001` | low | `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md` |
| `GO-API-002` | low | `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md` |
| `GO-API-003` | low | `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md` |
| `GO-API-006` | low | `11-semantic-duplication-migration-drift/15-api-response-boilerplate-duplication.md` |
| `GO-API-008` | low | `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md` |
| `GO-API-010` | low | `13-mechanical-cleanup/04-low-risk-error-handling-batch.md` |
| `GO-CHAT-001` | low | `11-semantic-duplication-migration-drift/10-envelope-registry-triplication.md` |
| `GO-CHAT-003` | low | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-CHAT-004` | low | `11-semantic-duplication-migration-drift/09-elicitation-client-side-duplication-and-dead-doc.md` |
| `GO-CHAT-005` | low | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-CHAT-006` | low | `13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md` |
| `GO-EXEC-004` | low | `11-semantic-duplication-migration-drift/16-workflow-naming-and-dispatch-naming-collisions.md` |
| `GO-INFRA-001` | low | `11-semantic-duplication-migration-drift/08-config-package-naming-collision.md` |
| `GO-INFRA-002` | low | `11-semantic-duplication-migration-drift/04-traffic-light-calculation-duplication.md` |
| `GO-INFRA-003` | low | `13-mechanical-cleanup/04-low-risk-error-handling-batch.md` |
| `GO-MCPTOOL-003` | low | `09-production-islands/06-curated-tool-knowledge-matcher.md` |
| `GO-MCPTOOL-004` | low | `11-semantic-duplication-migration-drift/09-elicitation-client-side-duplication-and-dead-doc.md` |
| `GO-MCPTOOL-009` | low | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-MCPTOOL-011` | low | `11-semantic-duplication-migration-drift/12-devservername-constant-duplication.md` |
| `GO-MCPTOOL-012` | low | `11-semantic-duplication-migration-drift/05-mcp-result-processing-tail-duplication.md` |
| `GO-MCPTOOL-013` | low | `13-mechanical-cleanup/04-low-risk-error-handling-batch.md` |
| `GO-MEM-002` | low | `09-production-islands/02-hadron-context-gate.md` |
| `GO-MEM-003` | low | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-MEM-006` | low | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-MEM-007` | low | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-MEM-009` | low | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-PLUGIN-005` | low | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-PLUGIN-007` | low | `11-semantic-duplication-migration-drift/14-adapter-plugin-boilerplate-duplication.md` |
| `GO-PLUGIN-008` | low | `01-plugin-install-convergence/02-wire-allow-unsigned-plugins-setting.md` |
| `GO-RUNTIME-001` | low | `07-runtime-correctness-lifecycle/02-fix-cmdserve-fatal-cleanup-bypass.md` |
| `GO-SEC-002` | low | `08-remaining-security-hardening/08-dependency-toolchain-vuln-bumps.md` |
| `GO-SEC4-005` | low | `02-linux-sandbox-fail-open/02-macos-seatbelt-read-boundary-disclosure.md` |
| `GO-SEC4-006` | low | `02-linux-sandbox-fail-open/01-sandbox-fail-closed-without-bwrap.md` |
| `GO-SEC4-008` | low | `08-remaining-security-hardening/06-sandbox-proxy-header-timeout.md` |
| `GO-STORE-004` | low | `06-store-correctness/02-triage-store-context-and-transaction-gaps.md` |
| `GO-STORE-006` | low | `06-store-correctness/02-triage-store-context-and-transaction-gaps.md` |
| `GO-STORE-007` | low | `11-semantic-duplication-migration-drift/13-store-scan-loop-duplication.md` |
| `GO-STORE-008` | low | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-SVCCORE-002` | low | `04-container-reaper-lifecycle/04-track-untracked-goroutine-spawns.md` |
| `GO-SVCCORE-008` | low | `13-mechanical-cleanup/03-naming-and-formatting-fixes.md` |
| `GO-SVCEXEC-006` | low | `13-mechanical-cleanup/04-low-risk-error-handling-batch.md` |
| `GO-SVCEXEC-007` | low | `13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md` |
| `GO-API-009` | informational | `11-semantic-duplication-migration-drift/15-api-response-boilerplate-duplication.md` |
| `GO-CHAT-007` | informational | `13-mechanical-cleanup/03-naming-and-formatting-fixes.md` |
| `GO-CHAT-008` | informational | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-CHAT-009` | informational | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-DEP-001` | informational | `10-architectural-concentration/03-container-and-store-review-note.md` |
| `GO-EXEC-003` | informational | `11-semantic-duplication-migration-drift/16-workflow-naming-and-dispatch-naming-collisions.md` |
| `GO-HYG-001` | informational | `12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md` |
| `GO-MCPTOOL-005` | informational | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-MCPTOOL-007` | informational | `10-architectural-concentration/02-selftoolstransport-decomposition.md` |
| `GO-MCPTOOL-010` | informational | `11-semantic-duplication-migration-drift/11-dispatch-reflex-double-evaluation.md` |
| `GO-MEM-004` | informational | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-MEM-005` | informational | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-MEM-008` | informational | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-PLUGIN-006` | informational | `10-architectural-concentration/03-container-and-store-review-note.md` |
| `GO-RUNTIME-005` | informational | `07-runtime-correctness-lifecycle/04-container-shutdown-idempotency-guard.md` |
| `GO-RUNTIME-006` | informational | `13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md` |
| `GO-RUNTIME-008` | informational | `13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md` |
| `GO-SEC4-009` | informational | `12-quality-ratchet-and-standards/03-goroutine-lint-coverage-gap.md` |
| `GO-SEC4-010` | informational | `13-mechanical-cleanup/04-low-risk-error-handling-batch.md` |
| `GO-STORE-002` | informational | `10-architectural-concentration/03-container-and-store-review-note.md` |
| `GO-STORE-009` | informational | `13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md` |
| `GO-SVCCORE-005` | informational | `12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md` |
| `GO-SVCCORE-007` | informational | `13-mechanical-cleanup/01-confirmed-dead-code-removal.md` |
| `GO-SVCCORE-009` | informational | `13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md` |

## Task file → findings (reverse lookup, grouped by folder)


### `01-plugin-install-convergence/`

- **`01-plugin-install-convergence/01-unify-plugin-catalog-install-pipeline.md`** — `GO-PLUGIN-001`, `GO-PLUGIN-002`, `GO-PLUGIN-003`
- **`01-plugin-install-convergence/02-wire-allow-unsigned-plugins-setting.md`** — `GO-PLUGIN-008`

### `02-linux-sandbox-fail-open/`

- **`02-linux-sandbox-fail-open/01-sandbox-fail-closed-without-bwrap.md`** — `GO-SEC4-001`, `GO-SEC4-002`, `GO-SEC4-006`
- **`02-linux-sandbox-fail-open/02-macos-seatbelt-read-boundary-disclosure.md`** — `GO-SEC4-005`

### `03-agent-slug-traversal/`

- **`03-agent-slug-traversal/01-canonical-slug-path-validation.md`** — `GO-AGENT-001`, `GO-AGENT-002`

### `04-container-reaper-lifecycle/`

- **`04-container-reaper-lifecycle/01-fix-container-constructor-partial-failure-cleanup.md`** — `GO-LIFE-001`
- **`04-container-reaper-lifecycle/02-fix-api-test-container-shutdown-leak.md`** — `GO-TEST-001`
- **`04-container-reaper-lifecycle/03-investigate-internal-service-race-timeout.md`** — `GO-SVCCORE-006`
- **`04-container-reaper-lifecycle/04-track-untracked-goroutine-spawns.md`** — `GO-SVCCORE-001`, `GO-SVCCORE-002`
- **`04-container-reaper-lifecycle/05-close-untested-service-config-functions.md`** — `GO-SVCEXEC-005`

### `05-subagent-execution-ordering/`

- **`05-subagent-execution-ordering/01-fix-approve-concurrency-cap-and-queued-cancel.md`** — `GO-EXEC-001`, `GO-EXEC-002`

### `06-store-correctness/`

- **`06-store-correctness/01-fix-deleteagentbyid-error-swallowing.md`** — `GO-STORE-003`
- **`06-store-correctness/02-triage-store-context-and-transaction-gaps.md`** — `GO-STORE-004`, `GO-STORE-005`, `GO-STORE-006`

### `07-runtime-correctness-lifecycle/`

- **`07-runtime-correctness-lifecycle/01-fix-worktree-orphan-branch-cleanup.md`** — `GO-RUNTIME-003`
- **`07-runtime-correctness-lifecycle/02-fix-cmdserve-fatal-cleanup-bypass.md`** — `GO-RUNTIME-001`
- **`07-runtime-correctness-lifecycle/03-bound-background-job-registry-growth.md`** — `GO-RUNTIME-004`
- **`07-runtime-correctness-lifecycle/04-container-shutdown-idempotency-guard.md`** — `GO-RUNTIME-005`
- **`07-runtime-correctness-lifecycle/05-fix-mcp-config-silent-decode-errors.md`** — `GO-RUNTIME-007`

### `08-remaining-security-hardening/`

- **`08-remaining-security-hardening/01-a2a-webhook-url-validation.md`** — `GO-SVCCORE-004`
- **`08-remaining-security-hardening/02-mcp-dev-grep-symlink-toctou.md`** — `GO-MCPTOOL-008`
- **`08-remaining-security-hardening/03-triage-remaining-gosec-g304-sites.md`** — `GO-SEC-003`
- **`08-remaining-security-hardening/04-permission-default-mode-write-gap.md`** — `GO-SEC4-003`
- **`08-remaining-security-hardening/05-secret-key-heuristic-hardening.md`** — `GO-SEC4-004`
- **`08-remaining-security-hardening/06-sandbox-proxy-header-timeout.md`** — `GO-SEC4-008`
- **`08-remaining-security-hardening/07-server-auth-bind-tls-posture.md`** — `GO-RUNTIME-002`
- **`08-remaining-security-hardening/08-dependency-toolchain-vuln-bumps.md`** — `GO-SEC-001`, `GO-SEC-002`
- **`08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md`** — `GO-API-001`, `GO-API-002`, `GO-API-003`, `GO-API-008`, `GO-SEC4-007`
- **`08-remaining-security-hardening/10-api-validation-duplication-and-pagination-bug.md`** — `GO-API-004`, `GO-API-005`

### `09-production-islands/`

- **`09-production-islands/01-grounding-memory-recall.md`** — `GO-MEM-001`
- **`09-production-islands/02-hadron-context-gate.md`** — `GO-MEM-002`
- **`09-production-islands/03-team-semantic-routing.md`** — `GO-SVCEXEC-003`
- **`09-production-islands/04-tool-builder-yaml-architecture.md`** — `GO-MCPTOOL-001`
- **`09-production-islands/05-reasoning-augmented-tool-selection.md`** — `GO-MCPTOOL-002`
- **`09-production-islands/06-curated-tool-knowledge-matcher.md`** — `GO-MCPTOOL-003`

### `10-architectural-concentration/`

- **`10-architectural-concentration/01-chatserviceimpl-generateresponse-decomposition.md`** — `GO-SVCEXEC-001`, `GO-SVCEXEC-002`
- **`10-architectural-concentration/02-selftoolstransport-decomposition.md`** — `GO-MCPTOOL-006`, `GO-MCPTOOL-007`
- **`10-architectural-concentration/03-container-and-store-review-note.md`** — `GO-DEP-001`, `GO-DEP-002`, `GO-STORE-001`, `GO-STORE-002`, `GO-PLUGIN-006`

### `11-semantic-duplication-migration-drift/`

- **`11-semantic-duplication-migration-drift/01-subagent-completion-vs-message-wake-policy.md`** — `GO-SVCEXEC-004`
- **`11-semantic-duplication-migration-drift/02-harness-v1-vs-native-durable-agent-handlers.md`** — `GO-API-007`
- **`11-semantic-duplication-migration-drift/03-structuredmessage-unwrap-duplication.md`** — `GO-CHAT-002`
- **`11-semantic-duplication-migration-drift/04-traffic-light-calculation-duplication.md`** — `GO-INFRA-002`
- **`11-semantic-duplication-migration-drift/05-mcp-result-processing-tail-duplication.md`** — `GO-MCPTOOL-012`
- **`11-semantic-duplication-migration-drift/06-provider-streaming-error-handling-divergence.md`** — `GO-INFRA-004`
- **`11-semantic-duplication-migration-drift/08-config-package-naming-collision.md`** — `GO-INFRA-001`
- **`11-semantic-duplication-migration-drift/09-elicitation-client-side-duplication-and-dead-doc.md`** — `GO-CHAT-004`, `GO-MCPTOOL-004`
- **`11-semantic-duplication-migration-drift/10-envelope-registry-triplication.md`** — `GO-CHAT-001`
- **`11-semantic-duplication-migration-drift/11-dispatch-reflex-double-evaluation.md`** — `GO-MCPTOOL-010`
- **`11-semantic-duplication-migration-drift/12-devservername-constant-duplication.md`** — `GO-MCPTOOL-011`
- **`11-semantic-duplication-migration-drift/13-store-scan-loop-duplication.md`** — `GO-STORE-007`
- **`11-semantic-duplication-migration-drift/14-adapter-plugin-boilerplate-duplication.md`** — `GO-PLUGIN-007`
- **`11-semantic-duplication-migration-drift/15-api-response-boilerplate-duplication.md`** — `GO-API-006`, `GO-API-009`
- **`11-semantic-duplication-migration-drift/16-workflow-naming-and-dispatch-naming-collisions.md`** — `GO-EXEC-003`, `GO-EXEC-004`

### `12-quality-ratchet-and-standards/`

- **`12-quality-ratchet-and-standards/01-full-repo-scheduled-lint-gate.md`** — `GO-HYG-001`, `GO-SVCCORE-005`
- **`12-quality-ratchet-and-standards/02-add-engineering-standards-docs.md`** — _(guide-derived, no specific finding IDs)_
- **`12-quality-ratchet-and-standards/03-goroutine-lint-coverage-gap.md`** — `GO-SEC4-009`

### `13-mechanical-cleanup/`

- **`13-mechanical-cleanup/01-confirmed-dead-code-removal.md`** — `GO-STORE-008`, `GO-AGENT-003`, `GO-MEM-003`, `GO-MEM-007`, `GO-MEM-008`, `GO-SVCCORE-007`, `GO-MCPTOOL-005`, `GO-CHAT-005`
- **`13-mechanical-cleanup/02-stale-comments-and-docs-cleanup.md`** — `GO-STORE-009`, `GO-SVCCORE-009`, `GO-SVCEXEC-007`, `GO-RUNTIME-006`, `GO-CHAT-006`, `GO-SVCCORE-003`
- **`13-mechanical-cleanup/03-naming-and-formatting-fixes.md`** — `GO-SVCCORE-008`, `GO-AGENT-004`, `GO-CHAT-007`
- **`13-mechanical-cleanup/04-low-risk-error-handling-batch.md`** — `GO-INFRA-003`, `GO-API-010`, `GO-SEC4-010`, `GO-SVCEXEC-006`, `GO-MCPTOOL-013`
- **`13-mechanical-cleanup/05-low-risk-hygiene-and-lock-scope-batch.md`** — `GO-MCPTOOL-009`, `GO-PLUGIN-005`, `GO-AGENT-005`, `GO-MEM-004`, `GO-MEM-005`, `GO-MEM-006`, `GO-MEM-009`, `GO-CHAT-003`, `GO-CHAT-008`, `GO-CHAT-009`, `GO-PLUGIN-004`, `GO-RUNTIME-008`
