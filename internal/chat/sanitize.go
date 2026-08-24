package chat

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// homeDir is cached at init so SanitizeToolError doesn't call os.UserHomeDir
// on every invocation.
var (
	homeDir     string
	homeDirOnce sync.Once
)

func getHomeDir() string {
	homeDirOnce.Do(func() {
		homeDir, _ = os.UserHomeDir()
	})
	return homeDir
}

// secretEnvSuffixes are environment variable name suffixes whose values should
// be redacted from tool error output. Best-effort defense-in-depth, not a
// security boundary.
var secretEnvSuffixes = []string{
	"_KEY", "_TOKEN", "_SECRET", "_PASS", "_PASSWORD",
}

// reUserDir matches absolute paths like /Users/<name>/… or /home/<name>/….
var reUserDir = regexp.MustCompile(`(?:/Users/|/home/)[^\s/]+(/[^\s]*)`)

// reGoStackFrame matches Go stack trace lines: goroutine N, filepath:line, and
// function names that follow.
var reGoStack = regexp.MustCompile(`(?m)^goroutine \d+ \[`)

// SanitizeToolError redacts sensitive information from tool error output.
// Rules applied (in order):
//  1. Replace the resolved home directory path with ~ everywhere in the string.
//  2. Replace /Users/<name>/… or /home/<name>/… paths with ~/….
//  3. Strip lines that look like secret env var assignments.
//  4. Truncate Go stack traces to the first frame.
func SanitizeToolError(raw string) string {
	if raw == "" {
		return raw
	}

	s := raw

	// Rule 1: literal home dir → ~.
	if hd := getHomeDir(); hd != "" {
		s = strings.ReplaceAll(s, hd, "~")
	}

	// Rule 2: any /Users/<name>/ or /home/<name>/ path → ~/<rest>.
	s = reUserDir.ReplaceAllString(s, "~$1")

	// Rule 3: strip secret env var lines.
	s = stripSecretEnvLines(s)

	// Rule 4: truncate Go stack traces to first frame.
	s = truncateGoStack(s)

	return s
}

// stripSecretEnvLines removes lines matching *_KEY=*, *_TOKEN=*, etc.
func stripSecretEnvLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isSecretEnvLine(trimmed) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isSecretEnvLine returns true if the line looks like NAME=VALUE where NAME
// ends with one of the secret suffixes.
func isSecretEnvLine(line string) bool {
	eqIdx := strings.IndexByte(line, '=')
	if eqIdx <= 0 {
		return false
	}
	key := strings.ToUpper(line[:eqIdx])
	for _, suffix := range secretEnvSuffixes {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

// truncateGoStack finds the first Go-style stack trace header ("goroutine N [")
// and keeps only the first frame (two lines after the header), replacing the
// rest with a sentinel.
func truncateGoStack(s string) string {
	loc := reGoStack.FindStringIndex(s)
	if loc == nil {
		return s
	}

	prefix := s[:loc[0]]
	rest := s[loc[0]:]

	lines := strings.SplitN(rest, "\n", 5) // header + 2 frame lines + potential more
	if len(lines) <= 3 {
		return s // single frame or less — nothing to truncate
	}

	// Keep header + first frame (2 lines), replace remainder.
	kept := strings.Join(lines[:3], "\n")
	return prefix + kept + "\n[... stack truncated]"
}
