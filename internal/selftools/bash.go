package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/tools"
)

type BashInput struct {
	Command        string `json:"command"`
	TimeoutSeconds *int   `json:"timeout_seconds"`
}

// ParseBash checks shell arguments and the hard denylist before authorization.
func ParseBash(raw json.RawMessage) (BashInput, error) {
	var input BashInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return BashInput{}, fmt.Errorf("invalid bash arguments: %w", err)
	}
	if strings.TrimSpace(input.Command) == "" {
		return BashInput{}, fmt.Errorf("bash command required")
	}
	if blocked, reason := shell.CheckDenylist(input.Command); blocked {
		return BashInput{}, fmt.Errorf("%s", reason)
	}
	if input.TimeoutSeconds != nil && (*input.TimeoutSeconds < 1 || *input.TimeoutSeconds > 600) {
		return BashInput{}, fmt.Errorf("timeout_seconds must be between 1 and 600")
	}
	return input, nil
}

func Bash(runner shell.Runner, dir string, env map[string]string) tools.Tool {
	return tools.Tool{Name: "bash", Description: "Run a Bash command from the workspace root with the current user's privileges.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":600,"default":120}},"required":["command"],"additionalProperties":false}`),
		Destructive: true,
		Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
			input, err := ParseBash(call.Input)
			if err != nil {
				return tools.Result{}, err
			}
			seconds := 120
			if input.TimeoutSeconds != nil {
				seconds = *input.TimeoutSeconds
			}
			result, err := runner.Run(ctx, shell.Command{Script: input.Command, Dir: dir, Env: env, Timeout: time.Duration(seconds) * time.Second})
			text := result.Output
			if err != nil {
				return tools.Result{Text: fmt.Sprintf("%s\nBash: %v", text, err), IsError: true}, nil
			}
			if result.ExitCode != 0 {
				return tools.Result{Text: fmt.Sprintf("%s\nBash exited with status %d", text, result.ExitCode), IsError: true}, nil
			}
			return tools.Result{Text: text}, nil
		},
	}
}
