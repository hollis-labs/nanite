// Package agentvalidation provides config validation for agent profiles.
// It lives in a separate package to avoid import cycles between store and toolclient.
package agentvalidation

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// ValidationResult holds the outcome of an agent config validation.
type ValidationResult struct {
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// OK returns true if there are no blocking errors.
func (v ValidationResult) OK() bool {
	return len(v.Errors) == 0
}

// Error returns a combined error string, or empty if OK.
func (v ValidationResult) Error() string {
	if v.OK() {
		return ""
	}
	return fmt.Sprintf("agent config validation failed: %s", strings.Join(v.Errors, "; "))
}

// ValidateAgentConfig checks an AgentProfile for configuration problems.
// Errors are blocking issues that should prevent creation/update.
// Warnings are non-blocking concerns that should be logged.
func ValidateAgentConfig(agent *store.AgentProfile) ValidationResult {
	var result ValidationResult

	// 1. Validate tool_permissions JSON is well-formed
	tp := strings.TrimSpace(agent.ToolPermissions)
	if tp != "" && tp != "{}" {
		var perms toolclient.ToolPermissions
		if err := json.Unmarshal([]byte(tp), &perms); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("tool_permissions is malformed JSON: %s", err.Error()))
		} else {
			// 2. Check for empty allow_list (explicitly set but empty)
			// We need to detect if allow_list was explicitly set to [].
			// Re-parse as raw map to check.
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tp), &raw); err == nil {
				if v, ok := raw["allow_list"]; ok {
					var list []string
					if json.Unmarshal(v, &list) == nil && len(list) == 0 {
						result.Warnings = append(result.Warnings,
							"allow_list is empty - this will deny all tools")
					}
				}
				// Also check shorthand "allow"
				if v, ok := raw["allow"]; ok {
					var list []string
					if json.Unmarshal(v, &list) == nil && len(list) == 0 {
						result.Warnings = append(result.Warnings,
							"allow_list is empty - this will deny all tools")
					}
				}
			}

			// 4. Validate glob patterns in allow_list and deny_list
			for _, pattern := range perms.AllowList {
				if err := validateGlobPattern(pattern); err != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("invalid glob pattern in allow_list %q: %s", pattern, err.Error()))
				}
			}
			for _, pattern := range perms.DenyList {
				if err := validateGlobPattern(pattern); err != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("invalid glob pattern in deny_list %q: %s", pattern, err.Error()))
				}
			}
		}
	}

	// 3. Warn if no MCP servers and permissive (empty) permissions
	mcpEmpty := isMCPServersEmpty(agent.MCPServers)
	permissive := isPermissionsPermissive(tp)
	if mcpEmpty && permissive {
		result.Warnings = append(result.Warnings,
			"agent has no MCP servers and permissive permissions - it will have no tools but unrestricted access")
	}

	// --- v2 field validation ---

	// 5. Validate tools (JSON string array with name/glob entries)
	if t := strings.TrimSpace(agent.Tools); t != "" && t != "[]" {
		var tools []string
		if err := json.Unmarshal([]byte(t), &tools); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("tools is malformed JSON array: %s", err.Error()))
		} else {
			for _, tool := range tools {
				if err := validateGlobPattern(tool); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("invalid tool pattern %q: %s", tool, err.Error()))
				}
			}
		}
	}

	// 6. Validate directories (JSON string array)
	if d := strings.TrimSpace(agent.Directories); d != "" && d != "[]" {
		var dirs []string
		if err := json.Unmarshal([]byte(d), &dirs); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("directories is malformed JSON array: %s", err.Error()))
		}
	}

	// 7. Validate constraints (JSON object with positive numeric values)
	if c := strings.TrimSpace(agent.Constraints); c != "" && c != "{}" {
		var constraints map[string]any
		if err := json.Unmarshal([]byte(c), &constraints); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("constraints is malformed JSON: %s", err.Error()))
		} else {
			allowedKeys := map[string]bool{"max_iterations": true, "max_time_seconds": true, "retry_budget": true}
			for k, v := range constraints {
				if !allowedKeys[k] {
					result.Warnings = append(result.Warnings, fmt.Sprintf("constraints: unknown key %q", k))
				}
				switch n := v.(type) {
				case float64:
					if n <= 0 {
						result.Errors = append(result.Errors, fmt.Sprintf("constraints.%s must be a positive number, got %v", k, n))
					}
				default:
					result.Errors = append(result.Errors, fmt.Sprintf("constraints.%s must be a number, got %T", k, v))
				}
			}
		}
	}

	// 8. Validate tags (JSON string array)
	if t := strings.TrimSpace(agent.Tags); t != "" && t != "[]" {
		var tags []string
		if err := json.Unmarshal([]byte(t), &tags); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("tags is malformed JSON array: %s", err.Error()))
		}
	}

	// 9. Validate status enum
	if s := agent.Status; s != "" && s != "active" && s != "disabled" {
		result.Errors = append(result.Errors, fmt.Sprintf("status must be 'active' or 'disabled', got %q", s))
	}

	// 10. Validate source enum
	validSources := map[string]bool{
		"": true, "system": true, "seed": true, "api": true, "agentrc": true,
		"crewai": true, "autogen": true, "import": true,
	}
	if !validSources[agent.Source] {
		result.Errors = append(result.Errors, fmt.Sprintf("source must be one of system/api/agentrc/crewai/autogen/import, got %q", agent.Source))
	}

	return result
}

// validateGlobPattern checks that a glob pattern has valid syntax.
// Patterns ending in * are prefix globs (always valid).
// Other patterns are checked via path.Match.
func validateGlobPattern(pattern string) error {
	if strings.HasSuffix(pattern, "*") {
		// Prefix glob — always syntactically valid
		return nil
	}
	_, err := path.Match(pattern, "test")
	if err != nil {
		return fmt.Errorf("bad glob syntax: %w", err)
	}
	return nil
}

// isMCPServersEmpty returns true if the mcp_servers field is empty or "[]".
func isMCPServersEmpty(mcpServers string) bool {
	s := strings.TrimSpace(mcpServers)
	return s == "" || s == "[]"
}

// isPermissionsPermissive returns true if tool_permissions is empty, "{}", or
// has neither allow_list nor deny_list set.
func isPermissionsPermissive(tp string) bool {
	s := strings.TrimSpace(tp)
	if s == "" || s == "{}" {
		return true
	}
	var perms toolclient.ToolPermissions
	if err := json.Unmarshal([]byte(s), &perms); err != nil {
		return false // malformed — not permissive
	}
	return len(perms.AllowList) == 0 && len(perms.DenyList) == 0
}
