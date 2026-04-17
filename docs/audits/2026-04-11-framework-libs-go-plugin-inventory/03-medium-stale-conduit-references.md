# [Medium] Stale "Conduit Host" references in Registry stubs

**Scope:** `framework/libs/go-plugin/registry.go`
**Topic:** Code hygiene
**Date:** 2026-04-11

## Problem

12 stub methods on `Registry` return error messages referencing "Conduit Host" (e.g., `"GetConfig not supported by shared Registry; use Conduit Host"`). The project was renamed from Conduit to Nanite. These error messages are user-visible in test output and debugging and reference a product name that no longer exists.

## Evidence

`framework/libs/go-plugin/registry.go:L243-244`:
```go
func (r *Registry) GetConfig(key string) (string, error) {
	return "", fmt.Errorf("GetConfig not supported by shared Registry; use Conduit Host")
}
```

Same pattern at lines 248-249, 253-254, 258-259, 262-263, 266-267, 272-273, 277-278, 282-283 (SetConfig, RegisterConfigSchema, RegisterConnector, RegisterProvider, RegisterCLIAdapter, RegisterCommand, RegisterSlot, RegisterKeybinding).

Also `doc.go:L7`:
```go
// expected from an out-of-tree host implementation such as Conduit Host.
```

## Impact

Confusing error messages for anyone debugging test failures or building a new host implementation. Low blast radius but signals stale naming across the SDK.

## Recommendation

Replace "Conduit Host" with "Nanite Host" (or just "the application host") in all error strings and the doc comment. A single `sed` pass:

```bash
sed -i '' 's/Conduit Host/Nanite Host/g' registry.go doc.go
```

## References

- `framework/libs/go-plugin/registry.go:L240-284`
- `framework/libs/go-plugin/doc.go:L7`
- Nanite rename tracked in `.nanite/agents/backend.md`
