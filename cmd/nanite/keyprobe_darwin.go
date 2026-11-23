package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// runSecurity runs /usr/bin/security and returns its output and exit code. Tests replace it:
// they must never touch the real keychain.
var runSecurity = func(ctx context.Context, args ...string) ([]byte, int, error) {
	out, err := exec.CommandContext(ctx, "/usr/bin/security", args...).CombinedOutput() // #nosec G204 -- fixed binary; no secret read
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// keychainHas reports whether the keychain holds service/account, reading its attributes only:
// `security find-generic-password` without -w or -g never reads the password, so it never asks
// for access, whatever the item's access control says.
func keychainHas(ctx context.Context, service, account string) (bool, bool, error) {
	out, code, err := runSecurity(ctx, "find-generic-password", "-s", service, "-a", account)
	switch {
	case err != nil:
		return false, true, fmt.Errorf("keychain lookup: %w", err)
	case code == 0:
		return true, true, nil
	case code == 44: // errSecItemNotFound
		return false, true, nil
	default:
		return false, true, fmt.Errorf("keychain lookup: exit %d: %s", code, strings.TrimSpace(string(out)))
	}
}
