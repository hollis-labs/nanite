package permission

import (
	"os"
	"os/user"
)

// HomeDir returns the user's home directory.
//
// `os.UserHomeDir()` reads `$HOME` first and only falls through to a
// passwd lookup on platforms where the env var is missing AND the
// stdlib has built-in support. On macOS-launchd-spawned services, the
// plist environment frequently omits HOME (see CW-20260502-0014:
// nanite-api-service inherits PATH and SSH_AUTH_SOCK only); without a
// fallback `os.UserHomeDir()` returns an error and any caller that
// expands `~/` silently flows the literal tilde through.
//
// We fall back to `user.Current()` because the cgo-backed
// getpwuid_r path resolves the user record even when HOME is unset.
//
// Returns ("", error) only when both probes fail — at that point the
// caller has no way to expand a tilde and should reject or skip the
// input rather than guess.
func HomeDir() (string, error) {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.HomeDir == "" {
		return "", os.ErrNotExist
	}
	return u.HomeDir, nil
}
