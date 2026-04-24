//go:build !windows

package provider

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/hollis-labs/nanite/internal/safego"
)

// PTYBridge is a provider that wraps CLI tools in pseudo-terminals.
// It spawns the CLI as a child process, reads its structured output,
// and maps events to Nanite's StreamEvent types.
type PTYBridge struct {
	adapter CLIAdapter
	cliPath string // resolved path to the CLI binary
}

// NewPTYBridge creates a PTY bridge for Claude CLI. Returns nil if the
// claude binary is not found in PATH. Preserved for backwards compatibility.
func NewPTYBridge() *PTYBridge {
	adapter := NewClaudeAdapter()
	path, ok := adapter.Detect()
	if !ok {
		return nil
	}
	return &PTYBridge{adapter: adapter, cliPath: path}
}

// NewPTYBridgeWithAdapter creates a PTY bridge for any CLI adapter.
func NewPTYBridgeWithAdapter(adapter CLIAdapter, cliPath string) *PTYBridge {
	return &PTYBridge{adapter: adapter, cliPath: cliPath}
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

// ptyToolOnlyError is the error message emitted when the CLI stream closes with
// tool_use blocks but zero text content. The nested CLI requested tools that
// Nanite's PTY adapter cannot forward to the tool broker.
const ptyToolOnlyError = "CLI bridge cannot forward tool calls — the nested CLI requested tools that cannot be proxied. Retry with an API provider for tool-heavy tasks."

// toolOnlyErrorEvent returns a StreamEvent signalling an unforwardable tool-use
// condition, or nil when the stream produced text content alongside tool calls.
// toolUseCount is the number of tool_use events seen; deltaCount is the number
// of delta (text) events seen. When the CLI emits tool_use blocks with no text
// the user would otherwise see a silent empty assistant row.
func toolOnlyErrorEvent(toolUseCount, deltaCount int) *StreamEvent {
	if toolUseCount > 0 && deltaCount == 0 {
		ev := StreamEvent{Type: "error", Error: ptyToolOnlyError}
		return &ev
	}
	return nil
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

	// Delegate arg construction to the adapter.
	cliSessionID, _ := CLISessionIDFromContext(ctx)
	args := p.adapter.BuildArgs(prompt, systemPrompt, cliSessionID)

	// Avoid logging full CLI arguments to prevent leaking user prompts or other sensitive data.
	slog.Info("pty: launching CLI", "adapter", p.adapter.Name(), "args", len(args))

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

	// Notify process tracker if one is attached.
	if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
		cb(cmd.Process, true)
	}

	ch := make(chan StreamEvent, 64)
	activityCb, hasActivity := ActivityCallbackFromContext(ctx)

	safego.Go(ctx, "provider.pty.readLoop", func() {
		defer close(ch)
		defer ptmx.Close()

		scanner := bufio.NewScanner(ptmx)
		// Set 1MB buffer for large tool results.
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		// Track tool_use and delta events to detect tool-only streams.
		var toolUseCount, deltaCount int

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

			events, err := p.adapter.ParseLine(line)
			if err != nil {
				// Raw CLI output line can contain user prompt / assistant
				// response text. Name attr "content" so the PII redactor
				// scrubs it in production; debug builds can see it.
				slog.Warn("pty: parse error", "adapter", p.adapter.Name(), "err", err, "content", string(line))
				continue
			}

			if len(events) > 0 && hasActivity && cmd.Process != nil {
				activityCb(cmd.Process.Pid)
			}

			for _, ev := range events {
				switch ev.Type {
				case "tool_use":
					toolUseCount++
				case "delta":
					deltaCount++
				}
				ch <- ev
			}
		}

		// Scanner finished — process has exited or PTY closed.
		if err := scanner.Err(); err != nil {
			// PTY read errors on process exit are expected (EIO).
			if !strings.Contains(err.Error(), "input/output error") {
				slog.Warn("pty: scanner error", "err", err)
			}
		}

		// Wait for process to finish.
		if err := cmd.Wait(); err != nil {
			if ctx.Err() == nil {
				// Only log if not a context cancellation.
				slog.Info("pty: process exited", "err", err)
			}
		}

		// Emit an error when the CLI requested tools but produced no text.
		// The PTY bridge has no broker passthrough (deferred post-beta), so the
		// user would otherwise see a silent empty assistant row. Surface a clear
		// failure instead. Skip when ctx is already cancelled — the caller will
		// see a cancellation error and a misleading tool-call message would be noise.
		if ctx.Err() == nil {
			if errEv := toolOnlyErrorEvent(toolUseCount, deltaCount); errEv != nil {
				slog.Warn("pty: tool-only stream — emitting error; broker passthrough not yet supported",
					"adapter", p.adapter.Name(), "tool_use_count", toolUseCount, "delta_count", deltaCount)
				ch <- *errEv
			}
		}

		// Notify process tracker that process has exited.
		if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
			cb(cmd.Process, false)
		}
	})

	return ch, nil
}

// killProcess sends SIGTERM, then SIGKILL after a grace period.
func (p *PTYBridge) killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	safego.Go(context.Background(), "provider.pty.killProcess.wait", func() {
		cmd.Wait()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}
