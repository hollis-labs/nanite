package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageAnnotator_AddsUsedWindowModelToRunFinish(t *testing.T) {
	rec := httptest.NewRecorder()
	ann := newUsageAnnotator(rec, "", func() (int, bool) { return 4321, true })
	frames := "id: 1\ndata: {\"v\":\"1\",\"seq\":1,\"verb\":\"run.start\",\"provider\":\"anthropic\",\"model\":\"claude-haiku-4-5\"}\n\n" +
		"data: {\"v\":\"1\",\"seq\":2,\"verb\":\"part.delta\",\"text\":\"hi\"}\n\n" +
		"data: {\"v\":\"1\",\"seq\":3,\"verb\":\"run.finish\",\"reason\":\"stop\",\"usage\":{\"scope\":\"final\",\"uncached_input\":100,\"output\":20}}\n\n"
	// split mid-line to prove buffering
	half := len(frames) / 2
	_, _ = ann.Write([]byte(frames[:half]))
	_, _ = ann.Write([]byte(frames[half:]))
	out := rec.Body.String()
	if !strings.Contains(out, `"verb":"part.delta","text":"hi"`) && !strings.Contains(out, `{"v":"1","seq":2,"verb":"part.delta","text":"hi"}`) {
		t.Fatalf("an untouched frame changed:\n%s", out)
	}
	var fin map[string]any
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, `"run.finish"`) {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "data: ")), &fin); err != nil {
				t.Fatalf("run.finish frame is not JSON: %v\n%s", err, l)
			}
		}
	}
	u, _ := fin["usage"].(map[string]any)
	if u["used"] != float64(4321) || u["model"] != "claude-haiku-4-5" || u["window"] == nil || u["window_source"] == nil {
		t.Fatalf("usage = %v", u)
	}
	if u["uncached_input"] != float64(100) || u["scope"] != "final" {
		t.Fatalf("existing usage fields must survive: %v", u)
	}
}

func TestUsageAnnotator_FallsBackToRunUsageAndDefaultWindow(t *testing.T) {
	rec := httptest.NewRecorder()
	ann := newUsageAnnotator(rec, "no-such-model-xyz", func() (int, bool) { return 0, false })
	_, _ = ann.Write([]byte("data: {\"verb\":\"run.finish\",\"usage\":{\"scope\":\"final\",\"uncached_input\":7,\"cache_read\":3,\"output\":2}}\n\n"))
	var fin map[string]any
	line := strings.Split(rec.Body.String(), "\n")[0]
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fin); err != nil {
		t.Fatal(err)
	}
	u := fin["usage"].(map[string]any)
	if u["used"] != float64(12) || u["window_source"] != "default" {
		t.Fatalf("usage = %v", u)
	}
}
