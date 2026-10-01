package service

// normalizeStopReason maps the output-truncation stop reasons providers and
// runtimes report onto the one the chat loop checks, "max_tokens":
// "length" (OpenAI-style) and "max_output_tokens" (OpenAI Responses). Other
// values pass through unchanged. The OpenAI adapters already map their own;
// this covers any runtime that does not (CW-20260930-0113). Runtime turns
// arrive normalized since go-agent-wrapper v0.17.0 / go-providers v0.35.0
// (llmtypes.NormalizeStopReason); the API-provider path still relies on this.
func normalizeStopReason(reason string) string {
	switch reason {
	case "length", "max_output_tokens":
		return "max_tokens"
	}
	return reason
}
