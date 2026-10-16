package service

import (
	"fmt"
	"strings"

	permissionlib "github.com/hollis-labs/substrate/harness/interception/permission"
)

// PermissionModeEnv names the environment variable that sets the permission
// engine's mode at boot. It overrides the permission_mode config key.
const PermissionModeEnv = "NANITE_PERMISSION_MODE"

// ResolveBootPermissionMode picks the permission engine's mode at boot from the
// environment value (wins) and the config-file value. Empty on both sides is the
// default mode. An unrecognized value returns an error and the default mode, so
// a typo can never widen access. Yolo is only returned when it was named.
func ResolveBootPermissionMode(configValue, envValue string) (permissionlib.Mode, string, error) {
	raw, source := strings.TrimSpace(configValue), "config"
	if v := strings.TrimSpace(envValue); v != "" {
		raw, source = v, "env"
	}
	if raw == "" {
		return permissionlib.ModeDefault, "default", nil
	}
	mode := permissionlib.Mode(strings.ToLower(raw))
	switch mode {
	case permissionlib.ModeDefault, permissionlib.ModeAcceptEdits, permissionlib.ModePlan, permissionlib.ModeYolo:
		return mode, source, nil
	}
	return permissionlib.ModeDefault, source, fmt.Errorf("invalid permission mode %q (from %s): must be default, accept-edits, plan or yolo", raw, source)
}
