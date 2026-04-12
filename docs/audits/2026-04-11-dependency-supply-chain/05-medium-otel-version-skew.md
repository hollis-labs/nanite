# [Medium] OpenTelemetry version skew between core and SDK/exporters

**Scope:** go.mod — Go dependency version alignment
**Topic:** Supply chain — version lag
**Date:** 2026-04-11

## Problem

The OpenTelemetry module family has a version skew: core packages are at v1.43.0 while the SDK and exporters are at v1.41.0. OTel's compatibility policy requires that the SDK version must be >= the core API version it implements.

## Evidence

`go.mod:14-15` (direct):
```
go.opentelemetry.io/otel v1.43.0
go.opentelemetry.io/otel/trace v1.43.0
```

`go.mod:66-69` (indirect):
```
go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.41.0
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.41.0
go.opentelemetry.io/otel/metric v1.43.0
go.opentelemetry.io/otel/sdk v1.41.0
```

The `otel` core API is v1.43.0 but `otel/sdk` is v1.41.0 — a 2-minor-version gap. The `metric` package is aligned at v1.43.0, but the trace SDK and exporters lag.

This skew is likely introduced by the `go-otel` sibling library (`../framework/libs/go-otel`) which depends on the older SDK version, while nanite's direct dependencies pull in the newer core API.

## Impact

OTel's API/SDK compatibility is designed to be forward-compatible (newer API, older SDK should work), so this is unlikely to cause runtime panics. However:

- New trace API features added in v1.42-v1.43 may silently no-op when the SDK is v1.41.
- Dependency resolution may behave unexpectedly if a future OTel release tightens version constraints.
- The skew makes it harder to reason about which OTel features are actually available at runtime.

## Recommendation

Update the `go-otel` sibling library to use OTel SDK v1.43.0 (matching the core API). Then run `go mod tidy` in nanite to align versions.

## References

- https://opentelemetry.io/docs/languages/go/
- `go.mod:14-15,65-69` — OTel version entries
- `../framework/libs/go-otel/go.mod` — sibling lib that likely pins the older SDK
