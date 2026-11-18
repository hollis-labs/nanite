package selftools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/tools"
)

type Ask func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func Question(ask Ask) tools.Tool {
	return spec("ask_user", "Ask the user a question during this turn.", `{"prompt":{"type":"string"},"schema":{"type":"object"}}`, []string{"prompt"}, false,
		func(ctx context.Context, args struct {
			Prompt string
			Schema json.RawMessage
		}) (string, error) {
			answer, err := ask(ctx, args.Prompt, args.Schema)
			if err != nil {
				return "", err
			}
			if string(answer) == "null" {
				return "", fmt.Errorf("user declined the question")
			}
			return string(answer), nil
		})
}
