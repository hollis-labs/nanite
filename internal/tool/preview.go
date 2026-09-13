package tool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// previewResult renders a bounded reading view. JSON paths refer to the
// original cached document; formatting never changes the stored result.
func previewResult(body string, budget int) (string, string) {
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var value any
	if json.Valid([]byte(body)) && decoder.Decode(&value) == nil {
		switch value.(type) {
		case map[string]any, []any:
			return previewJSON(value, "", budget, 0), "json"
		}
	}
	return previewText(body, budget), "text"
}

// previewText spends its budget on both ends, including an explicit omitted
// byte range. Prefer nearby newlines, but never discard most of a long line.
func previewText(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	if budget < 100 {
		return utf8Head(s, max(0, budget-3)) + utf8Head("...", max(0, budget))
	}
	room := budget - 100 // reserve the range marker
	headEnd := len(utf8Head(s, room/2))
	if nl := strings.LastIndexByte(s[:headEnd], '\n'); nl >= headEnd*3/4 {
		headEnd = nl
	}
	tailStart := len(s) - (room - room/2)
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	if nl := strings.IndexByte(s[tailStart:], '\n'); nl >= 0 && nl < (room-room/2)/4 {
		tailStart += nl + 1
	}
	return fmt.Sprintf("%s\n[OMITTED bytes %d..%d (%d bytes); ranges are end-exclusive]\n%s", s[:headEnd], headEnd, tailStart, tailStart-headEnd, s[tailStart:])
}

func utf8Head(s string, n int) string {
	if n >= len(s) {
		return s
	}
	n = max(0, n)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func pointerChild(path, key string) string {
	return path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func shortJSON(v any) (string, bool) {
	switch v.(type) {
	case map[string]any, []any:
		return "", false
	}
	b, _ := json.Marshal(v)
	return string(b), len(b) <= 180
}

func fieldRank(key string) int {
	// Common identity/outcome fields are useful across tools, not only tasks.
	switch strings.ToLower(key) {
	case "id", "name", "title", "status", "ok", "error", "iserror", "updated_at":
		return 0
	default:
		return 1
	}
}

func previewJSON(v any, path string, budget, depth int) string {
	label := strconv.Quote(path)
	if budget < len(label)+100 || depth >= 20 {
		return utf8Head(label+": [OMITTED; retrieve this JSON pointer]", max(0, budget))
	}
	switch node := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(node))
		for key := range node {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if fieldRank(keys[i]) != fieldRank(keys[j]) {
				return fieldRank(keys[i]) < fieldRank(keys[j])
			}
			return keys[i] < keys[j]
		})
		var out strings.Builder
		fmt.Fprintf(&out, "%s: object (%d fields)\n", label, len(keys))
		var large []string
		var omitted []string
		// Short fields share at most half the budget when larger children
		// exist. This keeps metadata from consuming the whole reading view.
		shortLimit := budget - 100
		for _, key := range keys {
			if _, short := shortJSON(node[key]); !short {
				large = append(large, key)
			}
		}
		if len(large) > 0 {
			shortLimit = budget / 2
		}
		for _, key := range keys {
			if text, short := shortJSON(node[key]); short {
				line := fmt.Sprintf("%q: %s\n", pointerChild(path, key), text)
				if out.Len()+len(line) <= shortLimit {
					out.WriteString(line)
				} else {
					omitted = append(omitted, key)
				}
			}
		}
		if len(omitted) > 0 {
			fmt.Fprintf(&out, "[OMITTED %d short fields: %s]\n", len(omitted), utf8Head(strings.Join(omitted, ", "), 80))
		}
		for i, key := range large {
			share := max(0, (budget-out.Len()-1)/(len(large)-i))
			out.WriteString(previewJSON(node[key], pointerChild(path, key), share, depth+1))
			out.WriteByte('\n')
		}
		return utf8Head(out.String(), budget)
	case []any:
		var out strings.Builder
		fmt.Fprintf(&out, "%s: array (%d items; indices refer to original order)\n", label, len(node))
		indices := make([]int, 0, 4)
		for i := 0; i < min(2, len(node)); i++ {
			indices = append(indices, i)
		}
		for i := max(2, len(node)-2); i < len(node); i++ {
			indices = append(indices, i)
		}
		if len(node) > len(indices) {
			fmt.Fprintf(&out, "[OMITTED items 2..%d, end-exclusive (%d items)]\n", len(node)-2, len(node)-len(indices))
		}
		for i, index := range indices {
			share := max(0, (budget-out.Len()-1)/(len(indices)-i))
			out.WriteString(previewJSON(node[index], pointerChild(path, strconv.Itoa(index)), share, depth+1))
			out.WriteByte('\n')
		}
		return utf8Head(out.String(), budget)
	case string:
		header := fmt.Sprintf("%s: text (%d UTF-8 bytes; offsets address decoded text)\n", label, len(node))
		return header + previewText(node, max(0, budget-len(header)))
	default:
		text, _ := json.Marshal(node)
		return label + ": " + previewText(string(text), budget-len(label)-2)
	}
}
