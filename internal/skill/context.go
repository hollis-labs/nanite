package skill

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// dynamicContextPattern matches !`command` markers in skill prompts.
var dynamicContextPattern = regexp.MustCompile("!`([^`]+)`")

// dynamicContextTimeout is the maximum time allowed for a single dynamic context command.
const dynamicContextTimeout = 10 * time.Second

// ResolveDynamicContext replaces all !`command` markers in a prompt with the
// output of executing those commands via /bin/sh. Commands run as subprocesses
// (not PTY) with a timeout. Failures are replaced with an error comment.
func ResolveDynamicContext(prompt, workingDir string) string {
	return dynamicContextPattern.ReplaceAllStringFunc(prompt, func(match string) string {
		sub := dynamicContextPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		cmd := sub[1]
		out, err := runContextCommand(cmd, workingDir)
		if err != nil {
			return fmt.Sprintf("<!-- skill context error: %s: %v -->", cmd, err)
		}
		return strings.TrimSpace(out)
	})
}

// runContextCommand executes a shell command and returns its combined output.
func runContextCommand(command, workingDir string) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", command)
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return buf.String(), fmt.Errorf("exit: %w", err)
		}
		return buf.String(), nil
	case <-time.After(dynamicContextTimeout):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("timeout after %s", dynamicContextTimeout)
	}
}
