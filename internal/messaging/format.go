package messaging

import "strings"

// FormatSubagentResultInjection renders unread kind=subagent_result
// messages as a <system-reminder> block for injection into
// SlotUserContext at turn start (CW-20260512-0019, Layer 1). Mirrors
// internal/reminders.FormatInjection's format. Returns an empty string
// when msgs is empty.
func FormatSubagentResultInjection(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	for _, m := range msgs {
		body := strings.TrimSpace(m.Body)
		if body == "" {
			body = "(no summary)"
		}
		b.WriteString("Subagent result (from ")
		b.WriteString(m.FromAgentID)
		b.WriteString("): ")
		b.WriteString(body)
		b.WriteString("\n")
	}
	b.WriteString("</system-reminder>")
	return b.String()
}
