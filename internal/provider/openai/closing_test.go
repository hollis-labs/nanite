package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/provider/toolchoice"
)

func TestClosingRequestKeepsToolsAndDisablesCalls(t *testing.T) {
	ctx := toolchoice.WithNone(t.Context())
	req := toolReq("gpt-4o")
	chat, err := buildChatParamsForContext(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	response, err := buildResponseParams(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []any{chat, response} {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"tool_choice":"none"`) || !strings.Contains(string(raw), `"tools":[`) {
			t.Fatalf("closing params: %s", raw)
		}
	}
}
