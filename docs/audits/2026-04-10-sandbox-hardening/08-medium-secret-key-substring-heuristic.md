# [Medium] Secret env-var heuristic over-blocks safe names and under-blocks real secrets

**Scope:** sandbox / environment filtering
**Topic:** Security — input validation, false positive + false negative heuristic
**Date:** 2026-04-10

## Problem

`isSecretKey` lowercases the env var name and checks for substring containment against `KEY`, `SECRET`, `TOKEN`, `PASSWORD`, `CREDENTIAL`, `AUTH`. This is used to filter:

- The entire os env before it reaches `UserExec` (`filterSecrets`).
- Caller-provided `Env` map in `AgentExec` (`buildAgentEnv`).

The substring heuristic has both false positives (safe names filtered out) and false negatives (real secret names passed through).

False positives (incorrectly filtered, sandboxed process cannot see them):

- `MONKEYPATCH` — contains `KEY`
- `JOURNEY_ID` — no match
- `TOKENIZER_CONFIG` — contains `TOKEN`
- `AUTHOR` — contains `AUTH`
- `CREDENTIALS_PATH_IS_EMPTY` — contains `CREDENTIAL`
- `SECRETARY` — contains `SECRET`
- `PATH_WITH_KEY_IN_NAME` — contains `KEY` (obvious)
- Developer-set local vars like `PERSONAL_KEY_LENGTH=8` get dropped silently

False negatives (incorrectly allowed, real secrets leak into the sandbox):

- `ANTHROPIC_BASE_URL` — if it contains an embedded credential (unlikely but not impossible), `BASE_URL` has no match
- `GH_PAT` — GitHub Personal Access Token. No match.
- `NPM_CONFIG__AUTH` — wait, contains `AUTH`. OK.
- `OPENAI_ORGANIZATION` — contains nothing on the list. Passed through. This is arguably not a secret, but it is PII.
- `PAGERDUTY_INTEGRATION_KEY` — contains `KEY`. Filtered. OK.
- `DATADOG_APP_KEY` — contains `KEY`. Filtered. OK.
- `SLACK_WEBHOOK_URL` — no match. The webhook URL IS the credential for Slack; posting to it is unauth'd write.
- `MAILGUN_PRIVATE_API_KEY` — `KEY`. Filtered. OK.
- `CLOUDFLARE_EMAIL`, `CLOUDFLARE_API_EMAIL` — no match. Email + API key = account access.
- `FIREBASE_CONFIG` (a JSON blob with an API key inside) — no match. Whole blob passes.
- `GOOGLE_APPLICATION_CREDENTIALS` — `CREDENTIAL`. Filtered. OK.
- `DATABASE_URL` — no match. Typically contains `postgres://user:password@host/db` — the password is inline. This is a huge miss. Many services put creds in `DATABASE_URL`, `REDIS_URL`, `AMQP_URL`, `ELASTICSEARCH_URL`.
- `NOTION_SECRET` — `SECRET`. Filtered.
- `GITHUB_APP_PRIVATE_KEY` — `KEY`. Filtered.
- `BEARER_TOKEN` — `TOKEN`. Filtered.

The biggest concrete miss is the `*_URL` family, because inline credentials in a DSN are standard. The biggest concrete false positive is anything with `KEY` in a non-secret context.

Additionally, the filter runs on env **names**, not values. A value that looks like a JWT or API key in an env var named `REQUEST_ID` passes through untouched. The filter cannot catch credential-shaped values in innocuously-named vars.

## Evidence

```go
// internal/sandbox/exec.go:36-52
var secretKeyPatterns = []string{
    "KEY",
    "SECRET",
    "TOKEN",
    "PASSWORD",
    "CREDENTIAL",
    "AUTH",
}

var minimalEnvKeys = []string{
    "HOME",
    "USER",
    "LANG",
    "TERM",
}
```

```go
// internal/sandbox/exec.go:249-258
func isSecretKey(name string) bool {
    upper := strings.ToUpper(name)
    for _, pattern := range secretKeyPatterns {
        if strings.Contains(upper, pattern) {
            return true
        }
    }
    return false
}
```

Test only covers the obvious cases (`exec_test.go:227-256`). Nothing exercises `DATABASE_URL` or the false-positive cases.

## Impact

- **Who:** anyone running Nanite with a dev env that includes `DATABASE_URL`, `REDIS_URL`, `SLACK_WEBHOOK_URL`, `OPENAI_ORGANIZATION`, or any other non-obvious-named credential. That's most developers.
- **What:** those credentials are forwarded to the sandboxed process. A prompt-injected agent can exfiltrate them via the proxy (even with the proxy bypass findings aside — the allowlist is attacker-influenceable if the agent can submit requests).
- **Blast radius:** depends on what the leaked cred grants. Database URLs with admin creds are worst-case; webhook URLs allow message spoofing. Organization IDs are PII but low-impact on their own.
- **False positive impact:** user-visible. A user with `CREDENTIALS_PATH=/tmp/…` env var will find the path not propagating into scripts, leading to confusing "file not found" errors inside the sandbox. Minor but irritating.

The reviewer context `.nanite/agents/reviewer-backend.md:180` explicitly calls out `SUPPORT_DATABASE_URL` as a secret that should not leak. The current filter does not catch this.

## Recommendation

Two-layered fix:

1. **Switch to an allowlist for `AgentExec`.** The current `AgentExec` ALREADY takes an allowlist approach via `minimalEnvKeys` for the host env, then overlays caller-provided `Env`. Good. The bug is in the overlay — caller-provided env vars are filtered through `isSecretKey`, which is the heuristic. Change the overlay to ALSO require explicit allowlisting: the caller provides a `map[string]string` and a boolean `trusted` flag; when `trusted=false` (the default), only keys matching `^[A-Z][A-Z0-9_]{0,31}$` AND not in an exact-match deny list are allowed. The deny list is a set, not a substring list:

   ```go
   var hardDenyEnvKeys = map[string]bool{
       "DATABASE_URL":               true,
       "REDIS_URL":                  true,
       "AMQP_URL":                   true,
       "ELASTICSEARCH_URL":          true,
       "SLACK_WEBHOOK_URL":          true,
       "GITHUB_TOKEN":               true,
       "GH_TOKEN":                   true,
       "GH_PAT":                     true,
       "OPENAI_API_KEY":             true,
       "ANTHROPIC_API_KEY":          true,
       "AWS_ACCESS_KEY_ID":          true,
       "AWS_SECRET_ACCESS_KEY":      true,
       "GOOGLE_APPLICATION_CREDENTIALS": true,
       "GOOGLE_API_KEY":             true,
       "NANITE_AUTH_USER":           true,
       "NANITE_AUTH_PASSWORD":       true,
       // ...
   }
   ```

   Combine with the current substring heuristic as a second check. Use both: substring-match blocks most obvious secret names, exact-match catches the ones the substring check misses.

2. **For `UserExec`, keep the current "filter the user's full env" model** (the user expects their tools to see their env), but:

   - Add the hardcoded exact-match deny list from (1) to the filter.
   - When `Sandboxed: true`, additionally inject a warning into the command's stderr if a known-secret-named env var was filtered — so the user knows why their script broke.
   - Document the filter behavior prominently in the shell API's user-facing docs.

3. **Log what was filtered** (at debug level, with values redacted) so the user can see why their env is different from what they expect, and so incident response can tell what was NOT filtered.

4. **Test coverage:** add table-driven tests for:
   - `DATABASE_URL` → filtered after fix
   - `REDIS_URL` → filtered after fix
   - `SLACK_WEBHOOK_URL` → filtered after fix
   - `MONKEYPATCH` → not filtered (false positive sanity)
   - `AUTHOR` → not filtered
   - `SECRETARY` → not filtered
   - `CREDENTIALS_DIR` → not filtered (but `*_CREDENTIALS` might be; document)

## References

- `internal/sandbox/exec.go:36-52, 209-258`
- `internal/sandbox/exec_test.go:227-256`
- `.nanite/agents/reviewer-backend.md:180` — enumerated sensitive env vars
- OWASP ASVS 8.3 — secrets storage / handling
- CWE-532 (insertion of sensitive information into log or env)
