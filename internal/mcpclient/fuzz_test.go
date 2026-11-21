package mcpclient

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// FuzzMCPTool: any server-supplied tool name and schema is admitted only
// under a model name providers accept, without panicking or hanging.
func FuzzMCPTool(f *testing.F) {
	for _, s := range [][3]string{
		{"srv", "echo", `{"type":"object"}`}, {"srv", "", `{"type":"object"}`}, {"", "x", `{"type":"object"}`},
		{"srv", "x", `null`}, {"srv", "x", `[]`}, {"srv", "x", `{"$ref":"#"}`},
		{"srv", "x", `{"$defs":{"a":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`}, {"srv", "日本語", `{"type":"object"}`},
		{"srv", "\xff\xfe", `{"type":"object"}`}, {"tool:pkg", "x", `{"type":"object"}`},
		{"a-very-long-server-name-for-testing", strings.Repeat("t", 40), `{"type":"object"}`},
		{strings.Repeat("s", 300), strings.Repeat("t", 300), `{"type":"object","properties":{"a":{"type":"string"}}}`},
	} {
		f.Add(s[0], s[1], []byte(s[2]))
	}
	f.Fuzz(func(t *testing.T, server, name string, schema []byte) {
		lease := &Lease{client: &Client{}}
		start := time.Now()
		tool, err := lease.tool(&registration{name: server}, &sdk.Tool{Name: name, InputSchema: json.RawMessage(schema)})
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("schema check took %v", elapsed)
		}
		if err == nil && !legalToolName.MatchString(tool.Name) {
			t.Fatalf("illegal model name %q (%d chars)", tool.Name, len(tool.Name))
		}
	})
}
