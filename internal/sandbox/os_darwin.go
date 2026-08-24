//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// seatbeltUnsafeRuneError is returned when a string destined for a seatbelt
// profile literal contains a byte the profile syntax cannot quote safely.
type seatbeltUnsafeRuneError struct {
	field  string
	value  string
	reason string
}

func (e *seatbeltUnsafeRuneError) Error() string {
	return fmt.Sprintf("sandbox: seatbelt: %s contains unsafe value: %s", e.field, e.reason)
}

// validateSeatbeltLiteral rejects strings that cannot be embedded in a
// TinyScheme string literal without changing the meaning of the profile.
// The seatbelt/TinyScheme parser treats `"`, `\`, parens, semicolons, and
// whitespace as structural; even backslash-escaping is unreliable across
// macOS releases, so we fail closed on any of these bytes. ASCII control
// chars (< 0x20) are always rejected.
func validateSeatbeltLiteral(field, value string) error {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 || c == 0x7f {
			return &seatbeltUnsafeRuneError{
				field:  field,
				value:  value,
				reason: fmt.Sprintf("control byte 0x%02x at offset %d", c, i),
			}
		}
		switch c {
		case '"', '\\', '(', ')', ';', '\'':
			return &seatbeltUnsafeRuneError{
				field:  field,
				value:  value,
				reason: fmt.Sprintf("forbidden byte %q at offset %d", c, i),
			}
		}
	}
	return nil
}

// seatbeltProfile generates a macOS sandbox-exec seatbelt profile.
// Strategy: allow default, then deny file writes outside sandbox and network.
// This is more practical than deny-default because macOS processes need many
// mach ports, sysctls, and IPC operations that are hard to enumerate.
//
// Any interpolated value is validated via validateSeatbeltLiteral before
// being written to the profile. Invalid values return a non-nil error and
// the caller must refuse to spawn the sandbox.
//
// CW-20260504-0003: extraWritePath, when non-empty, is appended to the
// file-write allow list as an additional subpath. Used by AgentExec to
// permit writes under a caller-supplied working_dir that's been gated
// by the upstream permission layer (e.g. the dev_tools path-grant store).
// Reads are unrestricted under (allow default), so this only affects
// the write boundary.
func seatbeltProfile(sandboxDir string, extraWritePath string, networkAllow []string) (string, error) {
	absDir, err := filepath.Abs(sandboxDir)
	if err != nil {
		absDir = sandboxDir
	}
	if err := validateSeatbeltLiteral("sandboxDir", absDir); err != nil {
		return "", err
	}
	var absExtra string
	if extraWritePath != "" {
		absExtra, err = filepath.Abs(extraWritePath)
		if err != nil {
			absExtra = extraWritePath
		}
		if err := validateSeatbeltLiteral("extraWritePath", absExtra); err != nil {
			return "", err
		}
	}
	// Even though the current profile only uses a fixed "localhost:*" host
	// pattern for network rules, validate every networkAllow entry so that a
	// future change interpolating these values into the profile cannot be
	// retrofitted into an injection vector.
	for i, dom := range networkAllow {
		if err := validateSeatbeltLiteral(fmt.Sprintf("networkAllow[%d]", i), dom); err != nil {
			return "", err
		}
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
	if absExtra != "" {
		fmt.Fprintf(&b, "      (subpath \"%s\")\n", absExtra)
	}
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

	return b.String(), nil
}

// applyOSSandbox wraps the command with macOS sandbox-exec for OS-level isolation.
// The original command becomes an argument to sandbox-exec.
// The returned cleanup function removes the temporary seatbelt profile file
// and should be called after the command finishes.
//
// CW-20260504-0003: extraWritePath, when non-empty, is added to the seatbelt
// profile's file-write allow list (in addition to sandboxDir + /tmp).
// Pass an empty string to preserve legacy sandbox-only write behavior.
//
// AD-01/AD-02 (TASKS/audit-remediation/ARCHITECT-DECISIONS.md, decided
// 2026-08-22): macOS is explicitly out of scope for both — sandbox-exec
// ships with the OS (no "tool missing" fail-open case exists here the way
// bwrap's absence does on Linux), and seatbelt's `(deny network-outbound)`
// / `(deny network-inbound)` pair is already kernel-enforced regardless of
// proxy mode, so there is no host-netns/allowlist inversion to fix. The
// proxyAddr parameter exists only for call-signature parity with
// os_linux.go/os_other.go; it is intentionally unused here. isolated is
// always true on success — real seatbelt isolation was applied.
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, extraWritePath string, networkAllow []string, proxyAddr string) (cleanup func(), isolated bool, err error) {
	_ = proxyAddr

	profile, err := seatbeltProfile(sandboxDir, extraWritePath, networkAllow)
	if err != nil {
		return nil, false, fmt.Errorf("build seatbelt profile: %w", err)
	}

	// Write profile to a temp file (sandbox-exec -f requires a file path).
	f, err := os.CreateTemp("", "nanite-seatbelt-*.sb")
	if err != nil {
		return nil, false, fmt.Errorf("create seatbelt profile: %w", err)
	}
	profilePath := f.Name()

	if _, err := f.WriteString(profile); err != nil {
		_ = f.Close()              // Preserve the write failure; close is cleanup for the incomplete profile.
		_ = os.Remove(profilePath) // Preserve the write failure; removal is cleanup for the incomplete profile.
		return nil, false, fmt.Errorf("write seatbelt profile: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(profilePath) // Preserve the close failure; removal is cleanup for the unusable profile.
		return nil, false, fmt.Errorf("close seatbelt profile: %w", err)
	}

	// Wrap: sandbox-exec -f <profile> <original-command> <original-args...>
	origPath := cmd.Path
	origArgs := cmd.Args[1:] // Args[0] is the command name

	cmd.Path = "/usr/bin/sandbox-exec"
	newArgs := make([]string, 0, 3+len(origArgs))
	newArgs = append(newArgs, "sandbox-exec", "-f", profilePath)
	newArgs = append(newArgs, origPath)
	newArgs = append(newArgs, origArgs...)
	cmd.Args = newArgs

	return func() {
		_ = os.Remove(profilePath) // The generated seatbelt profile is disposable after command execution.
	}, true, nil
}
