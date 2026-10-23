package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// scratchpadStateFile is the scratchpad's file under the session workspace's
// state/ directory.
const scratchpadStateFile = "scratchpad.json"

// attachScratchpadFile makes the scratchpad durable: it is read from
// <stateDir>/scratchpad.json now and rewritten on every change, so what an
// agent stores in one turn is there in the next (CW-20261022-0007). A missing
// file is an empty scratchpad. An unreadable, malformed or over-cap file is
// logged and ignored rather than failing the turn; the next write replaces it.
func (ls *loopState) attachScratchpadFile(stateDir string) {
	if stateDir == "" {
		return
	}
	ls.scratchpadPath = filepath.Join(stateDir, scratchpadStateFile)
	raw, err := os.ReadFile(ls.scratchpadPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		slog.Warn("scratchpad: not restored", "path", ls.scratchpadPath, "err", err)
		return
	}
	var entries map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		slog.Warn("scratchpad: ignoring malformed file", "path", ls.scratchpadPath, "err", err)
		return
	}
	total := 0
	for key, value := range entries {
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > scratchpadMaxValueBytes {
			slog.Warn("scratchpad: ignoring file with an oversize or unusable value", "path", ls.scratchpadPath, "key", key)
			return
		}
		total += len(encoded)
	}
	if total > scratchpadMaxTotalBytes {
		slog.Warn("scratchpad: ignoring oversize file", "path", ls.scratchpadPath, "bytes", total)
		return
	}
	ls.scratchpad, ls.scratchpadBytes = entries, total
	if ls.scratchpad == nil {
		ls.scratchpad = make(map[string]any)
	}
}

// persistScratchpad writes the scratchpad to its file (owner-only) through a
// rename, so a crash leaves the old file or the new one, never half of one. A
// loopState without a file has nothing to do.
func (ls *loopState) persistScratchpad() error {
	if ls.scratchpadPath == "" {
		return nil
	}
	b, err := json.Marshal(ls.scratchpad)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(ls.scratchpadPath), ".scratchpad-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), ls.scratchpadPath)
}
