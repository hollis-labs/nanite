// Package transcript is an experimental host-owned Claude-shape JSONL log.
// The host supplies an already-authorized root and a conversation ID. A cursor
// identifies committed bytes in this log; it is never a provider sequence.
package transcript

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

var (
	ErrBusy    = errors.New("transcript: conversation already has a writer")
	ErrInvalid = errors.New("transcript: invalid record or cursor")
	ErrGap     = errors.New("transcript: cursor gap or changed history")
	ErrTorn    = errors.New("transcript: torn tail")
)

const maxRecord = 4 << 20
const defaultSegmentBytes int64 = 16 << 20

// Cursor is opaque to callers. Segment and Offset locate the next record;
// Hash binds it to the last complete record before that position. Zero means
// the start of retained history. A cursor is durable only after Append returns.
type Cursor struct {
	Segment uint64 `json:"segment"`
	Offset  int64  `json:"offset"`
	Hash    string `json:"hash,omitempty"`
}

// Entry is one complete Claude-shaped JSONL object and its committed cursor.
type Entry struct {
	Record json.RawMessage
	Cursor Cursor
}

// Projection is called after a record is durable. If it fails, Append returns
// the committed cursor together with the error so the host can retry indexing.
// Projection failure never rolls back the transcript.
type Projection func(Entry) error

// Options selects segment size. A record larger than the limit gets its own
// segment, up to maxRecord. Zero selects 16 MiB. Each append is one write and
// one fsync; no async durability mode is exposed.
type Options struct {
	SegmentBytes int64
	Project      Projection
}

// Writer owns the exclusive OS file lock for one conversation. Close releases
// it. Hosts must also fence stale attempt generations before granting a writer.
type Writer struct {
	dir     string
	lock    *os.File
	file    *os.File
	segment uint64
	offset  int64
	hash    string
	limit   int64
	project Projection
}

// ConversationDir returns the directory for the host's path-safe conversation ID.
func ConversationDir(root, conversationID string) (string, error) {
	if root == "" || conversationID == "" || conversationID == "." || conversationID == ".." {
		return "", ErrInvalid
	}
	for _, r := range conversationID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", ErrInvalid
		}
	}
	return filepath.Join(root, conversationID), nil
}

// Open opens a new or existing log. It refuses a second writer and any torn
// or noncontiguous history; repair is a host decision, never an implicit trim.
func Open(root, conversationID string, options Options) (_ *Writer, err error) {
	dir, err := ConversationDir(root, conversationID)
	if err != nil {
		return nil, err
	}
	limit := options.SegmentBytes
	if limit == 0 {
		limit = defaultSegmentBytes
	}
	if limit < 1 {
		return nil, fmt.Errorf("%w: segment limit must be positive", ErrInvalid)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".writer.lock"), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- Uses a fixed lock filename beneath a validated conversation directory.
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115 -- File.Fd carries a Unix file descriptor.
		_ = lock.Close() // The lock acquisition error takes priority.
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	w := &Writer{dir: dir, lock: lock, limit: limit, project: options.Project}
	defer func() {
		if err != nil {
			_ = w.Close()
		}
	}()
	segments, err := segmentList(dir)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		w.segment = 1
		w.file, err = createSegment(dir, 1)
		return w, err
	}
	var last Cursor
	for _, segment := range segments {
		entries, scanErr := scanSegment(dir, segment, last.Hash)
		if scanErr != nil {
			return nil, scanErr
		}
		if len(entries) > 0 {
			last = entries[len(entries)-1].Cursor
		}
		if segment == segments[len(segments)-1] {
			w.segment = segment
		}
	}
	w.file, err = os.OpenFile(segmentPath(dir, w.segment), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, err
	}
	stat, err := w.file.Stat()
	if err != nil {
		return nil, err
	}
	w.offset = stat.Size()
	w.hash = last.Hash
	return w, nil
}

// Append validates a Claude-shape entry, writes exactly one line, fsyncs it,
// then returns its committed cursor. A short/failed write poisons the writer;
// reopening detects its torn tail. Unknown durable outcomes require replay.
func (w *Writer) Append(record json.RawMessage) (Cursor, error) {
	if w == nil || w.file == nil {
		return Cursor{}, ErrInvalid
	}
	line, err := canonicalRecord(record)
	if err != nil {
		return Cursor{}, err
	}
	if w.offset > 0 && w.offset+int64(len(line)) > w.limit {
		if err := w.rotate(); err != nil {
			return Cursor{}, err
		}
	}
	n, err := w.file.Write(line)
	if err != nil || n != len(line) {
		_ = w.file.Close()
		w.file = nil
		if err != nil {
			return Cursor{}, err
		}
		return Cursor{}, io.ErrShortWrite
	}
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		w.file = nil
		return Cursor{}, err
	}
	w.offset += int64(n)
	sum := sha256.Sum256(append([]byte(w.hash), line...))
	w.hash = hex.EncodeToString(sum[:])
	c := Cursor{Segment: w.segment, Offset: w.offset, Hash: w.hash}
	if w.project != nil {
		if err := w.project(Entry{Record: bytes.Clone(line[:len(line)-1]), Cursor: c}); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (w *Writer) rotate() error {
	if err := w.file.Close(); err != nil {
		w.file = nil
		return err
	}
	w.file = nil
	if w.segment == ^uint64(0) {
		return ErrInvalid
	}
	next := w.segment + 1
	f, err := createSegment(w.dir, next)
	if err != nil {
		return err
	}
	w.file = f
	w.segment = next
	w.offset = 0
	return nil
}

// Close releases the file and writer lease. It is safe to call more than once.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	var err error
	if w.file != nil {
		err = w.file.Close()
		w.file = nil
	}
	if w.lock != nil {
		if e := syscall.Flock(int(w.lock.Fd()), syscall.LOCK_UN); err == nil { // #nosec G115 -- File.Fd carries a Unix file descriptor.
			err = e
		}
		if e := w.lock.Close(); err == nil {
			err = e
		}
		w.lock = nil
	}
	return err
}

// Replay returns the exact suffix after a committed cursor. Missing segments,
// changed records, invalid offsets and torn tails are errors, never silent EOF.
// On ErrTorn, entries contains the verified complete suffix before the torn
// record. The caller can reconcile that committed prefix without repairing the
// file; Open still refuses to append until the tail is handled explicitly.
// An absent directory is an empty log only for the zero cursor.
func Replay(root, conversationID string, after Cursor) ([]Entry, error) {
	dir, err := ConversationDir(root, conversationID)
	if err != nil {
		return nil, err
	}
	segments, err := segmentList(dir)
	if errors.Is(err, os.ErrNotExist) && after == (Cursor{}) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		if after == (Cursor{}) {
			return nil, nil
		}
		return nil, ErrGap
	}
	if after != (Cursor{}) && (after.Segment == 0 || after.Offset <= 0 || len(after.Hash) != 64) {
		return nil, ErrInvalid
	}
	var out []Entry
	matched := after == (Cursor{})
	var previousHash string
	for _, segment := range segments {
		entries, e := scanSegment(dir, segment, previousHash)
		if e != nil && !errors.Is(e, ErrTorn) {
			return nil, e
		}
		if len(entries) > 0 {
			previousHash = entries[len(entries)-1].Cursor.Hash
		}
		for _, entry := range entries {
			if !matched {
				if entry.Cursor.Segment == after.Segment && entry.Cursor.Offset == after.Offset {
					if entry.Cursor.Hash != after.Hash {
						return nil, ErrGap
					}
					matched = true
				}
				continue
			}
			out = append(out, entry)
		}
		if e != nil {
			if !matched {
				return nil, ErrGap
			}
			return out, e
		}
	}
	if !matched {
		return nil, ErrGap
	}
	return out, nil
}

func canonicalRecord(record json.RawMessage) ([]byte, error) {
	if len(record) == 0 || len(record) > maxRecord-1 || !json.Valid(record) {
		return nil, ErrInvalid
	}
	var shape struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(record, &shape); err != nil {
		return nil, ErrInvalid
	}
	switch shape.Type {
	case "user", "assistant":
		if message := bytes.TrimSpace(shape.Message); len(message) == 0 || message[0] != '{' {
			return nil, ErrInvalid
		}
	case "system", "summary":
	default:
		return nil, ErrInvalid
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, record); err != nil {
		return nil, ErrInvalid
	}
	compact.WriteByte('\n')
	return compact.Bytes(), nil
}

func segmentPath(dir string, n uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d.jsonl", n))
}

func createSegment(dir string, n uint64) (*os.File, error) {
	f, err := os.OpenFile(segmentPath(dir, n), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	d, err := os.Open(dir) // #nosec G304 -- Opens the validated conversation directory for fsync.
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	if err = d.Sync(); err != nil {
		return nil, errors.Join(err, f.Close(), d.Close())
	}
	_ = d.Close()
	return f, nil
}

func segmentList(dir string) ([]uint64, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var segments []uint64
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		base := strings.TrimSuffix(name, ".jsonl")
		if len(base) != 20 || f.IsDir() {
			return nil, ErrGap
		}
		n, e := strconv.ParseUint(base, 10, 64)
		if e != nil || n == 0 || fmt.Sprintf("%020d", n) != base {
			return nil, ErrGap
		}
		segments = append(segments, n)
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })
	for i, n := range segments {
		if n != uint64(i+1) {
			return nil, ErrGap
		}
	}
	return segments, nil
}

func scanSegment(dir string, n uint64, previousHash string) ([]Entry, error) {
	f, err := os.Open(segmentPath(dir, n))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // Read-only segment.
	r := bufio.NewReader(f)
	var out []Entry
	var offset int64
	for {
		line, e := r.ReadBytes('\n')
		if e == io.EOF && len(line) == 0 {
			break
		}
		if e != nil || len(line) == 0 || line[len(line)-1] != '\n' {
			return out, ErrTorn
		}
		if len(line) > maxRecord || !json.Valid(line[:len(line)-1]) {
			return out, ErrTorn
		}
		offset += int64(len(line))
		sum := sha256.Sum256(append([]byte(previousHash), line...))
		previousHash = hex.EncodeToString(sum[:])
		out = append(out, Entry{Record: bytes.Clone(line[:len(line)-1]), Cursor: Cursor{Segment: n, Offset: offset, Hash: previousHash}})
	}
	return out, nil
}
