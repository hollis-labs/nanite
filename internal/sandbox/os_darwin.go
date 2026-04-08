//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// seatbeltProfile generates a macOS sandbox-exec seatbelt profile.
// Strategy: allow default, then deny file writes outside sandbox and network.
// This is more practical than deny-default because macOS processes need many
// mach ports, sysctls, and IPC operations that are hard to enumerate.
func seatbeltProfile(sandboxDir string, networkAllow []string) string {
	absDir, err := filepath.Abs(sandboxDir)
	if err != nil {
		absDir = sandboxDir
	}

	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n\n")

	// Deny file writes outside the sandbox directory and standard temp paths.
	b.WriteString("; Deny file writes outside sandbox\n")
	b.WriteString("(deny file-write*\n")
	b.WriteString("  (require-not\n")
	b.WriteString("    (require-any\n")
	fmt.Fprintf(&b, "      (subpath \"%s\")\n", absDir)
	b.WriteString("      (subpath \"/private/tmp\")\n")
	b.WriteString("      (subpath \"/tmp\")\n")
	b.WriteString("      (literal \"/dev/null\")\n")
	b.WriteString("      (literal \"/dev/tty\")\n")
	// Allow writing to /dev/fd/* for pipe operations.
	b.WriteString("      (subpath \"/dev/fd\")\n")
	b.WriteString("    )\n")
	b.WriteString("  )\n")
	b.WriteString(")\n\n")

	// Network rules: deny all or allow localhost only (for proxy).
	if len(networkAllow) == 0 {
		b.WriteString("; Deny network\n")
		b.WriteString("(deny network-outbound)\n")
		b.WriteString("(deny network-inbound)\n\n")
	} else {
		b.WriteString("; Allow network to localhost only (proxy runs on 127.0.0.1)\n")
		b.WriteString("(allow network-outbound\n")
		b.WriteString("  (remote ip \"localhost:*\"))\n")
		b.WriteString("(deny network-outbound)\n")
		b.WriteString("(deny network-inbound)\n\n")
	}

	return b.String()
}

// applyOSSandbox wraps the command with macOS sandbox-exec for OS-level isolation.
// The original command becomes an argument to sandbox-exec.
// The returned cleanup function removes the temporary seatbelt profile file
// and should be called after the command finishes.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
	profile := seatbeltProfile(sandboxDir, networkAllow)

	// Write profile to a temp file (sandbox-exec -f requires a file path).
	f, err := os.CreateTemp("", "nanite-seatbelt-*.sb")
	if err != nil {
		return nil, fmt.Errorf("create seatbelt profile: %w", err)
	}
	profilePath := f.Name()

	if _, err := f.WriteString(profile); err != nil {
		f.Close()
		os.Remove(profilePath)
		return nil, fmt.Errorf("write seatbelt profile: %w", err)
	}
	f.Close()

	// Wrap: sandbox-exec -f <profile> <original-command> <original-args...>
	origPath := cmd.Path
	origArgs := cmd.Args[1:] // Args[0] is the command name

	cmd.Path = "/usr/bin/sandbox-exec"
	newArgs := make([]string, 0, 3+len(origArgs))
	newArgs = append(newArgs, "sandbox-exec", "-f", profilePath)
	newArgs = append(newArgs, origPath)
	newArgs = append(newArgs, origArgs...)
	cmd.Args = newArgs

	return func() { os.Remove(profilePath) }, nil
}
