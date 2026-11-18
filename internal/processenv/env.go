// Package processenv defines the environment inherited by child processes.
package processenv

import (
	"os"
	"slices"
	"strings"
)

// Build merges host variables allowed in subprocesses with declared values.
func Build(declared map[string]string) map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "PATH", "HOME", "USER", "LANG", "TERM", "TMPDIR", "SHELL":
			env[key] = value
		default:
			if strings.HasPrefix(key, "LC_") {
				env[key] = value
			}
		}
	}
	for key, value := range declared {
		env[key] = value
	}
	return env
}

// List flattens an environment in stable key order.
func List(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	slices.Sort(out)
	return out
}
