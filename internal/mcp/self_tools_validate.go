package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/envelope"
)

// ToolSchemaLookup is the narrow registry surface nanite_validate uses to
// resolve a tool's input JSON schema by uniform name. *mcp.Manager
// satisfies it via LookupToolInputSchema; tests can substitute a stub.
//
// Returning (nil, true) is allowed: a tool with no declared input schema
// has nothing to validate. nanite_validate treats this as "any args
// accepted" — the same effective contract the MCP server would apply.
type ToolSchemaLookup interface {
	LookupToolInputSchema(uniformName string) (map[string]any, bool)
}

// validateToolDefinition returns the self-tool definition for
// nanite_validate. Kept in its own helper so the slice in
// selfToolDefinitions stays readable.
//
// The tool is read-only and side-effect-free: it never invokes the named
// tool, never persists, never opens panels. Failure modes are limited to
// "tool unknown" (registry miss) and "args don't match the schema"
// (validation fail).
func validateToolDefinition() Tool {
	return Tool{
		Name: "nanite_validate",
		Description: "Pre-flight check: validate a proposed tool-call's arguments against the tool's declared input schema, without invoking the tool. Cheap, deterministic, no side effects.\n\n" +
			"**When to use:** Before you fire a high-blast-radius tool (anything that mutates state, sends a message, opens a UI surface, or costs tokens) when you're unsure whether your `args` shape will be accepted. Also useful after a tool call returned a structured-error like \"missing required field\" — pass the same `args` here to get a structured fix list.\n\n" +
			"**When NOT to use:** Don't call this on every tool invocation reflexively — only when the schema is unfamiliar or the call is expensive. Pure read-only tools (search, list, get) typically don't justify a pre-flight; just call them and read the error if they reject the input.\n\n" +
			"**Output shape:** `{valid: bool, errors: [{path, reason, suggestion?}]}` where `path` is a JSON Pointer rooted at the args object (e.g. `/data/metrics/0/label`), `reason` is the leaf schema-violation message, and `suggestion` (when present) is a single-step fix hint such as `\"add required field 'metrics'\"` or `\"wrap in `[...]`\"`. On `valid: true`, errors is `[]`.\n\n" +
			"**Special case:** `nanite_show_card` validates `args.data` against the per-type envelope schema in addition to the top-level input schema, so the agent gets per-field feedback against the schema that actually rejects bad payloads (the top-level schema only declares `data: object`).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool_name": map[string]any{
					"type":        "string",
					"description": "The uniform agent-facing tool name to validate against (e.g. `nanite_show_card`, `dev_read`, `task_create`). Must be a tool currently registered with the MCP manager — pass an unknown name and the response is `{valid: false, errors: [{path: \"\", reason: \"unknown tool ...\"}]}`.",
				},
				"args": map[string]any{
					"type":        "object",
					"description": "The args object you would pass to the tool. Validated against the tool's declared input schema. For `nanite_show_card`, `args.data` is also validated against the envelope schema for `args.type` if both are present.",
				},
			},
			"required": []string{"tool_name", "args"},
		},
	}
}

// callValidate is the handler for nanite_validate. It implements Layer 2
// of the self-healing tool surface: schema-only pre-flight check.
//
// Resolution order for the schema:
//  1. Self-tools — found by walking selfToolDefinitions(). This includes
//     nanite_validate itself (idempotent self-introspection).
//  2. ToolSchemaLookup (the MCP manager) — every other registered tool
//     across every registered server.
//
// For nanite_show_card the handler additionally validates args.data
// against the envelope schema for args.type. The top-level input schema
// for nanite_show_card declares `data: object` (no per-type structure),
// so without this layer the validator would say "valid" for any object
// in `data`. The envelope schema is what actually rejects bad payloads
// at runtime in callShowCard, so it's the schema the agent needs to be
// pre-flighted against.
func (st *SelfToolsTransport) callValidate(_ context.Context, args map[string]any) (*ToolResult, error) {
	toolName, _ := args["tool_name"].(string)
	if toolName == "" {
		// Preserve the {valid, errors} uniform contract — a missing
		// tool_name is exactly the kind of mistake validate is supposed
		// to surface as a structured finding, not a hard error.
		return validationResult(false, []envelope.StructuredError{{
			Path:       "/tool_name",
			Reason:     "tool_name is required and must be a non-empty string",
			Suggestion: "pass the uniform agent-facing tool name, e.g. `nanite_show_card`",
		}}), nil
	}
	rawArgs, ok := args["args"].(map[string]any)
	if !ok {
		// Surface a structured error rather than a hard error — the
		// missing/wrong-type `args` field is the kind of mistake the
		// validator is supposed to catch, not crash on.
		return validationResult(false, []envelope.StructuredError{{
			Path:       "/args",
			Reason:     "args is required and must be a JSON object",
			Suggestion: "pass args as an object even when empty: `args: {}`",
		}}), nil
	}

	schema, found := st.lookupToolSchema(toolName)
	if !found {
		return validationResult(false, []envelope.StructuredError{{
			Path:   "",
			Reason: fmt.Sprintf("unknown tool %q (not registered)", toolName),
		}}), nil
	}

	var errs []envelope.StructuredError

	// Phase 1: validate args against the tool's top-level input schema.
	// nil/empty schema means "any args accepted" — match the runtime
	// contract: a tool that doesn't declare a schema doesn't get its
	// args checked.
	if len(schema) > 0 {
		schemaErrs, schemaErr := envelope.ValidateAgainstSchema(schema, rawArgs)
		if schemaErr != nil {
			// Schema itself is malformed. Surface it as a structured
			// error rather than a Go error so the agent gets a single
			// uniform shape regardless of what went wrong.
			return validationResult(false, []envelope.StructuredError{{
				Path:   "",
				Reason: fmt.Sprintf("tool %q schema is malformed: %v", toolName, schemaErr),
			}}), nil
		}
		errs = append(errs, schemaErrs...)
	}

	// Phase 2: nanite_show_card-specific deep validation. The top-level
	// schema declares `data: object` — the per-type envelope schema is
	// where the real contract lives.
	if toolName == "nanite_show_card" {
		envType, _ := rawArgs["type"].(string)
		data, hasData := rawArgs["data"].(map[string]any)
		// Only run the deep check when both fields are present and shaped
		// correctly; the top-level schema check above already flags
		// missing/mistyped `type` or `data`.
		if envType != "" && hasData {
			// Reject unsupported envelope types up front so the deep
			// error doesn't read as a schema-load failure.
			if !envelope.IsPassiveRenderable(envType) {
				errs = append(errs, envelope.StructuredError{
					Path:       "/type",
					Reason:     fmt.Sprintf("envelope type %q is not on the passive-renderable allow-list", envType),
					Suggestion: "allowed types: " + strings.Join(envelope.PassiveRenderableTypes, ", "),
				})
			} else {
				dataErrs, dataErr := envelope.ValidateEnvelopeData(envType, data)
				if dataErr != nil {
					errs = append(errs, envelope.StructuredError{
						Path:   "/data",
						Reason: fmt.Sprintf("schema for %q failed to load: %v", envType, dataErr),
					})
				} else {
					// Re-root the per-data errors at /data so the agent
					// sees a uniform pointer rooted at the args object.
					for _, e := range dataErrs {
						errs = append(errs, envelope.StructuredError{
							Path:       "/data" + e.Path,
							Reason:     e.Reason,
							Suggestion: e.Suggestion,
						})
					}
				}
			}
		}
	}

	return validationResult(len(errs) == 0, errs), nil
}

// lookupToolSchema resolves the input schema for a uniform tool name,
// preferring self-tool definitions (so the lookup works even when the
// MCP manager hasn't registered the self-server yet — useful in tests
// and during early init).
//
// Returns (nil, true) when the tool exists but declares no input schema
// — the caller treats that as "no validation, accept any args". Returns
// (nil, false) when the tool is genuinely unknown.
func (st *SelfToolsTransport) lookupToolSchema(toolName string) (map[string]any, bool) {
	for _, t := range selfToolDefinitions() {
		if t.Name == toolName {
			return t.InputSchema, true
		}
	}
	if st.SchemaLookup != nil {
		if schema, ok := st.SchemaLookup.LookupToolInputSchema(toolName); ok {
			return schema, true
		}
	}
	return nil, false
}

// validationResult builds the JSON-shaped tool result the LLM will read.
// Always returns IsError=false: a "valid: false" response is a successful
// validation call with negative findings, not a tool-execution failure.
func validationResult(valid bool, errs []envelope.StructuredError) *ToolResult {
	if errs == nil {
		errs = []envelope.StructuredError{}
	}
	out := map[string]any{
		"valid":  valid,
		"errors": errs,
	}
	body, err := json.Marshal(out)
	if err != nil {
		// Should be impossible — StructuredError is a flat struct and
		// the wrapper only carries primitives. Fall back to errorResult
		// rather than silently returning empty.
		return errorResult(fmt.Sprintf("validate: marshal result: %v", err))
	}
	return textResult(string(body))
}
