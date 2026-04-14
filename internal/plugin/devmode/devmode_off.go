//go:build !devmode

// Package devmode exposes a single compile-time constant, HostDevSigningBypass,
// that signing-enforcement call sites in internal/plugin, internal/plugin/catalog,
// and internal/plugin/install consult before deciding whether a missing or
// invalid signature is fatal.
//
// The value is fixed at build time by the `devmode` build tag — there is no
// runtime toggle. Production binaries (this file) compile with the constant
// hard-wired to false, which means:
//
//   - Catalog signatures are always verified.
//   - Per-plugin signatures are always verified.
//   - user_settings.allow_unsigned_plugins is intentionally inert: a
//     compromised settings row cannot disable signature verification in a
//     production binary.
//
// See devmode_on.go for the dev-build semantics.
package devmode

// HostDevSigningBypass is false in production builds. Catalog signatures and
// per-plugin signatures are ALWAYS verified. user_settings.allow_unsigned_plugins
// has NO effect in production — this is intentional. A compromised settings row
// cannot disable signature verification.
const HostDevSigningBypass = false
