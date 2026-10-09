package tool

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	toolresult "github.com/hollis-labs/go-toolresult"
)

var resultPointer = regexp.MustCompile(`tool_result://([A-Za-z0-9_-]+)`)
var resultFooter = regexp.MustCompile(`(?m)^\[TRUNCATED — [^\n]*tool_result://[^\n]*\]$`)

// ResultProjection reconciles cache references for one model-facing projection.
// It never changes stored results or transcripts, extends retention, or invokes
// a source tool. Availability is scoped and rechecked for each new projection.
type ResultProjection struct {
	ctx       context.Context
	cache     *toolresult.Cache
	sessionID string
	checked   map[string]string
	notices   []string
}

func NewResultProjection(ctx context.Context, cache *toolresult.Cache, sessionID string) *ResultProjection {
	return &ResultProjection{ctx: ctx, cache: cache, sessionID: sessionID, checked: make(map[string]string)}
}

func (p *ResultProjection) unavailable(id string) string {
	if notice, ok := p.checked[id]; ok {
		return notice
	}
	reason := "cache availability could not be checked"
	if p.cache != nil {
		// Read uses the same scope/expiry/body-stored checks as the fetch helper.
		// Only test availability; never expand a preview with the full cached body.
		_, err := p.cache.Read(p.ctx, p.sessionID, id, "", 0, 1, 1)
		switch {
		case err == nil:
			p.checked[id] = ""
			return ""
		case errors.Is(err, toolresult.ErrExpired):
			reason = "cached body expired"
		case errors.Is(err, toolresult.ErrNotFound):
			reason = "cached body missing or purged"
		case errors.Is(err, toolresult.ErrBodyNotStored):
			reason = "body exceeded the per-result storage cap and was not stored"
		}
	}
	notice := fmt.Sprintf("[CACHED RESULT UNAVAILABLE: %s; %s. The retained preview is incomplete evidence. Do not fetch/search this ID or infer omitted content. If still needed, request a fresh, narrower source query under current tool permissions.]", id, reason)
	p.checked[id] = notice
	p.notices = append(p.notices, notice)
	return notice
}

// Text removes stale recovery instructions as well as stale URI references.
// Unavailable pointers outside a generated footer get the same explicit marker.
func (p *ResultProjection) Text(text string) string {
	if !strings.Contains(text, "tool_result://") {
		return text
	}
	text = resultFooter.ReplaceAllStringFunc(text, func(footer string) string {
		match := resultPointer.FindStringSubmatch(footer)
		if len(match) == 2 {
			if notice := p.unavailable(match[1]); notice != "" {
				return notice
			}
		}
		return footer
	})
	return resultPointer.ReplaceAllStringFunc(text, func(pointer string) string {
		id := strings.TrimPrefix(pointer, "tool_result://")
		if notice := p.unavailable(id); notice != "" {
			return notice
		}
		return pointer
	})
}

// Notices correct references already held in an opaque provider-side history.
func (p *ResultProjection) Notices() string { return strings.Join(p.notices, "\n") }
