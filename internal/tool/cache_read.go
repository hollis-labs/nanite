package tool

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CachePage offsets are UTF-8 bytes relative to the selected value. An empty
// JSON pointer selects the untouched original result; a string selection is
// decoded (e.g. /stdout), while objects and arrays retain their JSON bytes.
type CachePage struct {
	Content     string
	Offset      int
	End         int
	TotalBytes  int
	HasMore     bool
	JSONPointer string
}

func (c *ResultCache) selectedBody(sessionID, id, pointer string) (string, error) {
	body, _, err := c.Fetch(sessionID, id, 0, 0)
	if err != nil || pointer == "" {
		return body, err
	}
	if !strings.HasPrefix(pointer, "/") {
		return "", fmt.Errorf("json_pointer must be empty or start with /")
	}
	if !json.Valid([]byte(body)) {
		return "", fmt.Errorf("cached result is not JSON; omit json_pointer")
	}
	raw := json.RawMessage(body)
	for _, part := range strings.Split(pointer[1:], "/") {
		for i := 0; i < len(part); i++ {
			if part[i] == '~' {
				if i+1 >= len(part) || (part[i+1] != '0' && part[i+1] != '1') {
					return "", fmt.Errorf("invalid JSON pointer escape")
				}
				i++
			}
		}
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil && object != nil {
			var ok bool
			raw, ok = object[key]
			if !ok {
				return "", fmt.Errorf("JSON pointer %q: field %q not found", pointer, key)
			}
			continue
		}
		var array []json.RawMessage
		if json.Unmarshal(raw, &array) == nil && array != nil {
			index, parseErr := strconv.Atoi(key)
			if parseErr != nil || index < 0 || index >= len(array) || strconv.Itoa(index) != key {
				return "", fmt.Errorf("JSON pointer %q: invalid array index %q", pointer, key)
			}
			raw = array[index]
			continue
		}
		return "", fmt.Errorf("JSON pointer %q traverses a scalar", pointer)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && string(raw) != "null" {
		return text, nil
	}
	return string(raw), nil
}

func (c *ResultCache) ReadPage(sessionID, id, pointer string, offset, length, budget int) (CachePage, error) {
	body, err := c.selectedBody(sessionID, id, pointer)
	if err != nil {
		return CachePage{}, err
	}
	if offset < 0 {
		return CachePage{}, fmt.Errorf("offset must be non-negative")
	}
	budget = max(4, min(budget, c.hardCapBytes))
	if length <= 0 || length > budget {
		length = budget
	}
	start := min(offset, len(body))
	for start > 0 && start < len(body) && !utf8.RuneStart(body[start]) {
		start--
	}
	end := start + min(length, len(body)-start)
	for end > start && end < len(body) && !utf8.RuneStart(body[end]) {
		end--
	}
	if end == start && start < len(body) {
		_, width := utf8.DecodeRuneInString(body[start:])
		end += width // Always make progress, even if length split one rune.
	}
	return CachePage{Content: body[start:end], Offset: start, End: end, TotalBytes: len(body), HasMore: end < len(body), JSONPointer: pointer}, nil
}

type CacheSearchMatch struct {
	Line             int
	MatchOffset      int
	MatchEnd         int
	ContextOffset    int
	ContextEnd       int
	Context          string
	ContextTruncated bool
}

type CacheSearchPage struct {
	Matches     []CacheSearchMatch
	TotalBytes  int
	HasMore     bool
	NextOffset  int
	JSONPointer string
}

// SearchPage searches matching lines, with bounded context and explicit
// continuation. Coordinates use the same selected representation as ReadPage.
func (c *ResultCache) SearchPage(sessionID, id, pointer, pattern string, offset, maxMatches, budget int) (CacheSearchPage, error) {
	body, err := c.selectedBody(sessionID, id, pointer)
	if err != nil {
		return CacheSearchPage{}, err
	}
	if offset < 0 {
		return CacheSearchPage{}, fmt.Errorf("offset must be non-negative")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return CacheSearchPage{}, fmt.Errorf("invalid search pattern: %w", err)
	}
	maxMatches = min(100, max(1, maxMatches))
	budget = max(512, min(budget, c.hardCapBytes))
	out := CacheSearchPage{TotalBytes: len(body), JSONPointer: pointer, NextOffset: len(body)}
	if offset >= len(body) {
		return out, nil
	}
	lines := strings.SplitAfter(body, "\n")
	starts := make([]int, len(lines)+1)
	for i, line := range lines {
		starts[i+1] = starts[i] + len(line)
	}
	remaining := budget - 256
	for i, line := range lines {
		start := starts[i]
		if starts[i+1] <= offset {
			continue
		}
		from := min(len(line), max(0, offset-start))
		match := re.FindStringIndex(strings.TrimSuffix(line[from:], "\n"))
		if match == nil {
			continue
		}
		if len(out.Matches) >= maxMatches || remaining < 256 {
			out.HasMore, out.NextOffset = true, start+from
			break
		}
		matchStart, matchEnd := start+from+match[0], start+from+match[1]
		contextStart := starts[max(0, i-2)]
		contextEnd := starts[min(len(lines), i+3)]
		allowance := min(2000, remaining-200)
		clipped := contextEnd-contextStart > allowance
		if clipped {
			contextStart = max(contextStart, matchStart-allowance/3)
			contextEnd = min(contextEnd, contextStart+allowance)
		}
		for contextStart < contextEnd && !utf8.RuneStart(body[contextStart]) {
			contextStart++
		}
		for contextEnd > contextStart && contextEnd < len(body) && !utf8.RuneStart(body[contextEnd]) {
			contextEnd--
		}
		text := body[contextStart:contextEnd]
		out.Matches = append(out.Matches, CacheSearchMatch{Line: i + 1, MatchOffset: matchStart, MatchEnd: matchEnd, ContextOffset: contextStart, ContextEnd: contextEnd, Context: text, ContextTruncated: clipped})
		remaining -= len(text) + 200
	}
	return out, nil
}
