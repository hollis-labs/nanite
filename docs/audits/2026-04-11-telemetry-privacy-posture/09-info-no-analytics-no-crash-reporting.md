# [Info] No analytics SDKs, no crash reporting, no phone-home behavior

**Scope:** Whole codebase
**Topic:** Analytics / error reporting
**Date:** 2026-04-11

## Problem

No issue. This is a positive finding.

## Evidence

### Backend (Go)

Searched the entire Go codebase for:
- Error reporting SDKs: `sentry`, `bugsnag`, `rollbar`, `errorreporting`, `crashreport`, `phone-home` — **no matches** in Go files.
- Analytics SDKs: `mixpanel`, `amplitude`, `posthog`, `segment`, `datadog`, `newrelic` — **no matches** in Go files (the word "analytics" appears only in the `brand.go` OTel service name and telemetry-related code reviewed separately).
- No `go.mod` dependencies on any analytics or error-reporting libraries.

### Frontend (React/TS)

Searched `ui/src/`:
- `analytics`, `mixpanel`, `posthog`, `amplitude`, `segment`, `gtag`, `google-analytics` — **one match**: `EditableStringList.tsx` uses the word "analytics" in a generic context (not an SDK).
- `sentry`, `bugsnag`, `rollbar`, `logrocket`, `hotjar`, `fullstory`, `heap` — **one match**: `ui/src/index.css` mentions "rollbar" in a CSS comment (not an SDK import).
- No external CDN resources: searched for `fonts.googleapis`, `cdn.`, `cloudflare`, `unpkg`, `jsdelivr` — **no matches**.

### Frontend API client

`ui/src/lib/api.ts` makes all calls to the local nanite backend (`/api/*`). No external API calls from the frontend.

## Impact

Nanite has zero analytics, zero crash reporting, and zero phone-home behavior. The only outbound calls are those explicitly needed for functionality (LLM providers, embedding, OTel, activity emitter, catalog fetch, oEmbed, Giphy).

## Recommendation

This is the correct design for a local-first tool. No action required. If crash reporting is ever added, it should follow the same opt-in pattern as the other outbound calls.

## References

- Full codebase grep for analytics/error-reporting patterns
- `ui/src/lib/api.ts` — all frontend network calls route through the local backend
