package service

// normalizeStopReason maps the output-truncation stop reasons providers and
// runtimes report onto the one the chat loop checks, "max_tokens":
// "length" (OpenAI-style) and "max_output_tokens" (OpenAI Responses). Other
// values pass through unchanged. The OpenAI adapters already map their own;
// this covers any runtime that does not (CW-20260930-0113). Stopgap until the
// libs normalize stop reasons (CW-20260930-0228).
func normalizeStopReason(reason string) string {
	switch reason {
	case "length", "max_output_tokens":
		return "max_tokens"
	}
	return reason
}
