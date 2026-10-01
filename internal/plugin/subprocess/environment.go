package subprocess

import "strings"

// pluginEnvironment keeps ambient credentials and authentication handles out of
// plugin processes. Explicit entries are supplied by trusted host code; declared
// plugin secrets belong in the init config channel rather than ambient env.
func pluginEnvironment(parent, explicit []string) []string {
	// Keep this non-nil even for an empty parent: nil exec.Cmd.Env inherits
	// the host environment, while an empty slice starts with no environment.
	env := make([]string, 0, len(parent)+len(explicit))
	for _, entry := range parent {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "PATH", "HOME", "TMPDIR", "TMP", "TEMP", "USER", "LOGNAME", "LANG", "LC_ALL", "XDG_RUNTIME_DIR":
			env = append(env, entry)
		}
	}
	return append(env, explicit...)
}
