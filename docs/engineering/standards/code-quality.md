# Code Quality

Stub. What this review's evidence base actually supports as real, established discipline here:

- **Dead code gets removed, not flagged for later.** Standing policy — see `../GLOSSARY.md`'s dead-code-adjacent entries and `user/chrispian/memory/decisions/aggressive_dead_code_removal_policy`. Zero callers or zero rows is grounds for outright removal by default in this codebase specifically (single operator, no external dependents to break), not a universal rule for every codebase.
- **Verify a claim about the code by reading the code, not by trusting a prior audit's or a doc's characterization of it.** This review found several concrete cases where a prior pass's claim about "dead" or "unwired" code was simply wrong (`session_handoffs`, most notably — real, wired CRUD, incorrectly called dead-with-zero-store-methods in one audit doc). Docs and audits drift from reality; the code doesn't.
- **A silent fail-open is worse than a loud failure.** The single most repeated defect shape found across this codebase's real incident history — a fallback, a swallowed error, or a default that silently substitutes for the real thing, with nothing surfacing that it happened. Prefer a clear, visible failure over a graceful-looking one that masks a real problem.
- **Don't let a config value or feature flag exist as a hardcoded compile-time constant with "no config surface threads a non-default value into production" as its stated behavior.** Several real subsystems in this codebase reached exactly that state — a knob that looks configurable in code but has no actual path for anyone to configure it. If it's not really tunable, don't shape it like it is.

## Not yet documented

Static analysis / linting tooling beyond `go vet`, PR review checklist, a written definition of "done" beyond "tests pass" (see `testing.md`'s note on dogfooding).
