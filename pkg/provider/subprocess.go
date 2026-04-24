package provider

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/hollis-labs/nanite/internal/safego"
)

// SubprocessBridge is a provider that wraps CLI tools using standard pipes
// (stdin/stdout) instead of pseudo-terminals. It works on all platforms
// including Windows where PTY support is unavailable.
//
// The trade-off vs PTYBridge: some CLIs detect non-TTY stdout and may
// alter their output format or disable interactive features. For CLIs
// that support explicit output format flags (e.g. --output-format stream-json),
// this is generally not an issue.
type SubprocessBridge struct {
	adapter CLIAdapter
	cliPath string
}

// NewSubprocessBridge creates a subprocess bridge for any CLI adapter.
func NewSubprocessBridge(adapter CLIAdapter, cliPath string) *SubprocessBridge {
	return &SubprocessBridge{adapter: adapter, cliPath: cliPath}
}

func (s *SubprocessBridge) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	return s.streamCLI(ctx, systemPrompt, messages)
}

func (s *SubprocessBridge) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	return s.streamCLI(ctx, systemPrompt, messages)
}

func (s *SubprocessBridge) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	ctx = context.WithValue(ctx, ptySessionKeyType{}, "")
	ch, err := s.streamCLI(ctx, systemPrompt, messages)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "delta":
			sb.WriteString(ev.Content)
		case "error":
			return "", fmt.Errorf("cli error: %s", ev.Error)
		}
	}
	return sb.String(), nil
}

func (s *SubprocessBridge) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,
		SupportsPreToolHooks:        false,
		SupportsPostToolHooks:       false,
		SupportsSystemPromptCaching: false,
		SupportsToolCalling:         true,
		SupportsBatch:               false,
		SupportsImageInput:          false,
		MaxTokens:                   0,
	}
}

// streamCLI spawns the CLI as a subprocess with piped stdout and streams parsed events.
func (s *SubprocessBridge) streamCLI(ctx context.Context, systemPrompt string, messages []ChatMessage) (<-chan StreamEvent, error) {
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

	cliSessionID, _ := CLISessionIDFromContext(ctx)
	args := s.adapter.BuildArgs(prompt, systemPrompt, cliSessionID)

	slog.Info("subprocess: launching CLI", "adapter", s.adapter.Name(), "args", len(args))

	cmd := exec.CommandContext(ctx, s.cliPath, args...)

	if dir, ok := SandboxDirFromContext(ctx); ok {
		cmd.Dir = dir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start subprocess: %w", err)
	}

	// Notify process tracker if one is attached.
	if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
		cb(cmd.Process, true)
	}

	ch := make(chan StreamEvent, 64)
	activityCb, hasActivity := ActivityCallbackFromContext(ctx)

	safego.Go(ctx, "provider.subprocess.readLoop", func() {
		defer close(ch)
		defer stdout.Close()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		// Track tool_use and text-delta events to detect the tool-only
		// response case: CLI requested tools the subprocess bridge cannot forward.
		var seenToolUse, seenDelta int

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: "error", Error: "context cancelled"}
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				return
			default:
			}

			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			events, err := s.adapter.ParseLine(line)
			if err != nil {
				// Raw CLI output line can contain user prompt / assistant
				// response text. Name attr "content" so the PII redactor
				// scrubs it in production.
				slog.Warn("subprocess: parse error", "adapter", s.adapter.Name(), "err", err, "content", string(line))
				continue
			}

			if len(events) > 0 && hasActivity && cmd.Process != nil {
				activityCb(cmd.Process.Pid)
			}

			for _, ev := range events {
				switch ev.Type {
				case "tool_use":
					seenToolUse++
				case "delta":
					seenDelta++
				}
				ch <- ev
			}
		}

		if err := scanner.Err(); err != nil {
			slog.Warn("subprocess: scanner error", "adapter", s.adapter.Name(), "err", err)
		}

		if err := cmd.Wait(); err != nil {
			if ctx.Err() == nil {
				slog.Info("subprocess: process exited", "adapter", s.adapter.Name(), "err", err)
			}
		}

		// Detect tool-only response: the nested CLI requested tools that
		// Nanite's subprocess bridge has no path to forward. Without this
		// check, the stream closes silently and the user sees an empty row.
		// Emit a visible error so the UI can surface a clear failure message.
		if seenToolUse > 0 && seenDelta == 0 && ctx.Err() == nil {
			slog.Warn("subprocess: tool-only response — CLI requested tools the subprocess bridge cannot forward",
				"adapter", s.adapter.Name(), "tool_use_count", seenToolUse)
			ch <- StreamEvent{
				Type:  "error",
				Error: "CLI bridge cannot forward tool calls — the nested CLI requested tools that cannot be proxied. Retry with an API provider for tool-heavy tasks.",
			}
		}

		// Notify process tracker that process has exited.
		if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
			cb(cmd.Process, false)
		}
	})

	return ch, nil
}
