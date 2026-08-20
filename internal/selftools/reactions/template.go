package reactions

import (
	"fmt"
	"regexp"
)

// placeholderPattern matches a {{key}} template placeholder — the "simple
// {{key}} string substitution" TASKS/harness-reactive-self-tools/
// 03-reaction-engine-core.md's "What to do" item 2 calls sufficient for
// this batch (explicitly not nested templating, conditionals, auth,
// retries, or idempotency — see this folder's README, "What this batch
// does NOT do").
var placeholderPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

// substituteTemplate walks a decoded-JSON value (map[string]any, []any,
// string, or any other json.Unmarshal leaf type) and replaces every
// {{key}} placeholder found inside a string value with
// fmt.Sprint(payload[key]). A placeholder whose key is absent from payload
// is left as the literal "{{key}}" text rather than silently blanked, so a
// misconfigured/missing field stays visible in the resolved output instead
// of disappearing.
//
// Shared by both execution shapes that need it: render_card's template
// (render_card.go's ResolveRenderCard) and internal_api_call's
// body_template (internal_api_call.go's executeInternalAPICall).
func substituteTemplate(v any, payload map[string]any) any {
	switch val := v.(type) {
	case string:
		return placeholderPattern.ReplaceAllStringFunc(val, func(match string) string {
			key := placeholderPattern.FindStringSubmatch(match)[1]
			replacement, ok := payload[key]
			if !ok {
				return match
			}
			return fmt.Sprint(replacement)
		})
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = substituteTemplate(vv, payload)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = substituteTemplate(vv, payload)
		}
		return out
	default:
		return v
	}
}
