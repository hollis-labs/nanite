package mcp

// Tool represents an MCP tool definition returned by tools/list.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	// Annotations are the MCP spec's behavior hints — readOnlyHint,
	// destructiveHint, idempotentHint, openWorldHint. They are how a server
	// tells a client that a tool changes something, which is what an approval
	// gate should key off. Inferring it from the tool's name instead works
	// until it doesn't, and the way it fails is letting a write through.
	Annotations map[string]any `json:"annotations,omitempty"`
}

// ToolResult represents the result of a tools/call invocation.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// ToolContent represents a content block in a tool result.
type ToolContent struct {
	Type string `json:"type"` // text, image, resource
	Text string `json:"text,omitempty"`
}
