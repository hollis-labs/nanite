# [Medium] Provider API responses parsed without content validation — gap in provider-abstractions coverage

**Scope:** Provider — trust boundary #11 (Outbound HTTP), partially #4 (MCP tool results analogue)
**Topic:** Security
**Date:** 2026-04-11

## Problem

HTTP API providers (Anthropic, OpenAI, Ollama, Gemini, Mistral, Azure, OpenRouter, OpenZen) parse SSE/JSON responses from LLM APIs and map them to `StreamEvent` structs. The response content is trusted without validation. A compromised or MITM'd provider API could inject:
- Crafted tool_use events with arbitrary tool names and arguments
- Manipulated usage/token counts
- Content containing prompt injection payloads targeting downstream tool execution

## Evidence

The `provider-abstractions` audit (2026-04-11) covered unbounded response bodies (High — OOM risk) and the Gemini API key in URL (High). However, it did not cover **content-level validation** of the parsed responses.

All providers parse response JSON and map it to `StreamEvent` with the same trust-everything pattern. For example, the Anthropic adapter parses `tool_use` content blocks from the SSE stream and maps them to `StreamEvent.ToolUse` with the tool name and arguments taken directly from the API response.

The gap vs. `provider-abstractions`: that audit focused on HTTP-level issues (body size, TLS, key exposure). This finding addresses the application-level trust: once the response is parsed, the content flows into the chat engine's tool-use loop without any verification that the tool names match the tools that were sent in the request, or that the arguments conform to the tool schemas.

This is the same class of issue as finding 03 (PTY output no validation) but through the HTTP API path rather than the PTY path. The HTTP API path is generally more trustworthy (authenticated TLS to known endpoints) but is not immune to:
- Provider-side compromise
- DNS hijacking / BGP hijacking
- Middleware MITM (corporate proxy, dev tools)

## Impact

Lower severity than PTY (finding 03) because HTTP API providers connect over TLS to known endpoints. But the defense gap is the same: tool_use events from provider responses are executed without verifying they match the tools that were offered. A compromised provider response could invoke arbitrary tools.

Severity is Medium because the attack requires compromising the TLS connection to a major LLM provider, which is a high-bar prerequisite.

## Recommendation

1. In the tool-use loop (`chat_generate.go`), validate that tool_use events reference tool names from the `tools` list that was sent in the request. Reject or warn on unknown tool names.
2. This validation should apply uniformly to all provider types (HTTP API and PTY), creating a single trust boundary enforcement point in the chat engine rather than per-provider.

## References

- Cross-ref: `provider-abstractions` audit — HTTP-level findings (body size, key exposure)
- Cross-ref: finding 03 (PTY output no validation) — same class, different transport
- Cross-ref: `toolbroker` — tool arguments passed through unvalidated
