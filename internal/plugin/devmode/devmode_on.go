//go:build devmode

package devmode

// HostDevSigningBypass is true in dev builds (go build -tags devmode).
//
// Catalog signatures are unconditionally bypassed in dev builds — the
// catalog signed-fetcher shortcut path treats this constant as permission
// to accept an unsigned / unverifiable catalog.
//
// Per-plugin signatures are bypassed ONLY when the operator also opts in
// via user_settings.allow_unsigned_plugins = true. The setting is honoured
// only in devmode builds; in production builds (HostDevSigningBypass ==
// false) the setting is ignored entirely.
const HostDevSigningBypass = true
