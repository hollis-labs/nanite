package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/pkg/models"
)

// usageAnnotator rewrites the chatstream/v1 SSE of a native turn so every usage
// carrier says how full the context is: usage.used, usage.window and usage.model
// (plus usage.window_source, "catalog" or "default"). Tachyon draws its context
// meter from these instead of a constant. Only run.finish and usage events are
// touched, and only by adding keys; every other byte passes through unchanged.
//
//	used   tokens in the context after the run's last model call (input incl.
//	       cache + output); falls back to the usage the event itself carries
//	window the model's context window from the catalog (Station alias overlay
//	       aware); "default" source means the model is not in the catalog
//	model  the model that answered
type usageAnnotator struct {
	http.ResponseWriter
	model       string
	contextUsed func() (int, bool)
	buf         []byte
}

func newUsageAnnotator(w http.ResponseWriter, model string, contextUsed func() (int, bool)) *usageAnnotator {
	return &usageAnnotator{ResponseWriter: w, model: model, contextUsed: contextUsed}
}

// Unwrap lets http.ResponseController reach the real writer (flush, deadlines).
func (u *usageAnnotator) Unwrap() http.ResponseWriter { return u.ResponseWriter }

func (u *usageAnnotator) Write(p []byte) (int, error) {
	u.buf = append(u.buf, p...)
	var out bytes.Buffer
	for {
		i := bytes.IndexByte(u.buf, '\n')
		if i < 0 {
			break
		}
		line := u.buf[:i]
		out.Write(u.annotateLine(line))
		out.WriteByte('\n')
		u.buf = u.buf[i+1:]
	}
	if out.Len() > 0 {
		if _, err := u.ResponseWriter.Write(out.Bytes()); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (u *usageAnnotator) Flush() {
	if f, ok := u.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// annotateLine returns the line unchanged unless it is a data frame of a
// run.finish or usage event.
func (u *usageAnnotator) annotateLine(line []byte) []byte {
	const prefix = "data:"
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return line
	}
	payload := bytes.TrimPrefix(line, []byte(prefix))
	if !bytes.Contains(payload, []byte(`"run.start"`)) && !bytes.Contains(payload, []byte(`"run.finish"`)) && !bytes.Contains(payload, []byte(`"verb":"usage"`)) {
		return line
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(payload)))
	dec.UseNumber()
	var ev map[string]any
	if err := dec.Decode(&ev); err != nil {
		return line
	}
	verb, _ := ev["verb"].(string)
	switch verb {
	case "run.start":
		if m, _ := ev["model"].(string); m != "" {
			u.model = m
		}
		return line
	case "run.finish", "usage":
	default:
		return line
	}
	usage, _ := ev["usage"].(map[string]any)
	if usage == nil {
		if verb == "usage" {
			return line
		}
		usage = map[string]any{}
	}
	used := sumUsage(usage)
	if verb == "run.finish" && u.contextUsed != nil {
		if v, ok := u.contextUsed(); ok {
			used = v
		}
	}
	if used > 0 {
		usage["used"] = used
	}
	if u.model != "" {
		usage["model"] = u.model
		usage["window"] = models.ContextWindowFor(u.model)
		if _, ok := models.ByModelID(u.model); ok {
			usage["window_source"] = "catalog"
		} else {
			usage["window_source"] = "default"
		}
	}
	ev["usage"] = usage
	body, err := json.Marshal(ev)
	if err != nil {
		return line
	}
	sep := " "
	if !strings.HasPrefix(string(payload), " ") {
		sep = ""
	}
	return append([]byte(prefix+sep), body...)
}

func sumUsage(usage map[string]any) int {
	total := 0
	for _, k := range []string{"uncached_input", "cache_read", "cache_write", "output", "reasoning"} {
		if n, ok := usage[k].(json.Number); ok {
			if v, err := n.Int64(); err == nil {
				total += int(v)
			}
		}
	}
	return total
}
