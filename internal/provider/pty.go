//go:build !windows

package provider

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// PTYBridge is a provider that wraps CLI tools in pseudo-terminals.
// It spawns the CLI as a child process, reads its structured output,
// and maps events to Conduit's StreamEvent types.
type PTYBridge struct {
	cliPath string // resolved path to the CLI binary
}

// NewPTYBridge creates a PTY bridge provider. Returns nil if the claude
// CLI binary is not found in PATH.
func NewPTYBridge() *PTYBridge {
	// Allow override via env var.
	cliPath := os.Getenv("CLAUDE_CLI_PATH")
	if cliPath == "" {
		var err error
		cliPath, err = exec.LookPath("claude")
		if err != nil {
			return nil
		}
	}
	return &PTYBridge{cliPath: cliPath}
}

func (p *PTYBridge) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return p.streamCLI(ctx, systemPrompt, messages)
}

func (p *PTYBridge) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	// Claude CLI manages its own tools — ignore the tools parameter.
	return p.streamCLI(ctx, systemPrompt, messages)
}

func (p *PTYBridge) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	// Complete is always single-turn — strip any resume session ID.
	ctx = context.WithValue(ctx, ptySessionKeyType{}, "")
	ch, err := p.streamCLI(ctx, systemPrompt, messages)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "delta":
			sb.WriteString(ev.Content)
		case "error":
			return "", fmt.Errorf("claude cli error: %s", ev.Error)
		}
	}
	return sb.String(), nil
}

func (p *PTYBridge) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,
		SupportsPreToolHooks:        false,
		SupportsPostToolHooks:       false,
		SupportsSystemPromptCaching: false, // CLI manages its own caching
		SupportsToolCalling:         true,  // CLI handles tools internally
		SupportsBatch:               false,
		SupportsImageInput:          false, // CLI stdin limitation
		MaxTokens:                   0,     // CLI manages its own limits
	}
}

// streamCLI spawns the Claude CLI in a PTY and streams parsed events.
func (p *PTYBridge) streamCLI(ctx context.Context, systemPrompt string, messages []ChatMessage) (<-chan StreamEvent, error) {
	// Extract the last user message as the prompt.
	var prompt string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != "" {
			prompt = messages[i].Content
			break
		}
	}
	if prompt == "" {
		return nil, fmt.Errorf("no user message found")
	}

	// Build command args.
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
	}

	// If we have a CLI session ID from a previous turn, resume it.
	// The CLI already has the system prompt from the first turn.
	if cliSessionID, ok := CLISessionIDFromContext(ctx); ok {
		args = append([]string{"--resume", cliSessionID}, args...)
	} else if systemPrompt != "" {
		// Only pass system prompt on the first message (fresh session).
		args = append(args, "--system-prompt", systemPrompt)
	}

	log.Printf("pty: args=%v", args)

	cmd := exec.CommandContext(ctx, p.cliPath, args...)

	// Run in the sandbox directory if one was provided.
	if dir, ok := SandboxDirFromContext(ctx); ok {
		cmd.Dir = dir
	}

	// Start in a PTY.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)
		defer ptmx.Close()

		scanner := bufio.NewScanner(ptmx)
		// Set 1MB buffer for large tool results.
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: "error", Error: "context cancelled"}
				p.killProcess(cmd)
				return
			default:
			}

			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			events, err := parseClaudeStreamLine(line)
			if err != nil {
				log.Printf("pty: parse error: %v (line: %s)", err, string(line))
				continue
			}

			for _, ev := range events {
				ch <- ev
			}
		}

		// Scanner finished — process has exited or PTY closed.
		if err := scanner.Err(); err != nil {
			// PTY read errors on process exit are expected (EIO).
			if !strings.Contains(err.Error(), "input/output error") {
				log.Printf("pty: scanner error: %v", err)
			}
		}

		// Wait for process to finish.
		if err := cmd.Wait(); err != nil {
			if ctx.Err() == nil {
				// Only log if not a context cancellation.
				log.Printf("pty: process exited: %v", err)
			}
		}
	}()

	return ch, nil
}

// killProcess sends SIGTERM, then SIGKILL after a grace period.
func (p *PTYBridge) killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}
