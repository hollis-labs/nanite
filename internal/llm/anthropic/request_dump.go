package anthropic

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// requestDumpEnv names a directory; when set, every outgoing Messages request
// body is written there as one JSON file. It exists to diagnose provider-side
// refusals and rejections, where the only useful evidence is the exact bytes
// sent. The bodies contain conversation content, so it is off by default and
// the files are written owner-only.
const requestDumpEnv = "NANITE_PROVIDER_REQUEST_DUMP_DIR"

var requestDumpSeq atomic.Uint64

// dumpRequest writes payload to the configured directory and returns the file
// path, or "" when dumping is off or the write failed (never fatal).
func dumpRequest(model string, payload []byte) string {
	dir := os.Getenv(requestDumpEnv)
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("provider: request dump dir unusable", "dir", dir, "err", err)
		return ""
	}
	name := fmt.Sprintf("%s-%06d-%s.json", time.Now().UTC().Format("20060102T150405.000Z"), requestDumpSeq.Add(1), model)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		slog.Warn("provider: request dump write failed", "path", path, "err", err)
		return ""
	}
	return path
}
