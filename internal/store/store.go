package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"sync"
	"time"

	core "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

var ErrNoConversation = errors.New("nanite: no conversation")

//go:embed migrations/sqlite/*.sql
var migrations embed.FS

type Store struct {
	db          *core.Store
	owned       bool
	transcripts string

	tornMu sync.Mutex
	torn   map[string]bool // conversations whose last replay stopped at a torn tail
}

// SettingIntegrity is the conversation setting that marks a conversation an
// integrity check found unsafe ("blocked"). It outlives a restart, so a
// conversation stays refused until a check passes again.
const SettingIntegrity = "integrity"

// TornConversations lists, in order, the conversations whose transcript
// ended in a torn tail when they were last replayed. Replay on open reads
// every transcript, so this costs no extra scan.
func (s *Store) TornConversations() []string {
	s.tornMu.Lock()
	defer s.tornMu.Unlock()
	out := make([]string, 0, len(s.torn))
	for id := range s.torn {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (s *Store) markTorn(id string, torn bool) {
	s.tornMu.Lock()
	defer s.tornMu.Unlock()
	if s.torn == nil {
		s.torn = map[string]bool{}
	}
	if torn {
		s.torn[id] = true
	} else {
		delete(s.torn, id)
	}
}

func Open(ctx context.Context, dataDir string, db *core.Store) (*Store, error) {
	s := &Store{db: db, transcripts: filepath.Join(dataDir, "transcripts")}
	if db == nil {
		if err := PreparePrivateDB(dataDir); err != nil {
			return nil, err
		}
		opened, err := core.Open(ctx, core.Config{Driver: core.DriverSQLite, SQLite: core.SQLiteConfig{Path: filepath.Join(dataDir, "nanite.db")}})
		if err != nil {
			return nil, err
		}
		s.db, s.owned = opened, true
	}
	config := s.db.Config()
	if config.Driver != core.DriverSQLite {
		_ = s.Close()
		return nil, fmt.Errorf("nanite: conversation store requires SQLite")
	}
	if err := migrate.Ensure(ctx, config, s.db, migrate.Source{FS: migrations, Root: "migrations", Table: "nanite_schema_version"}); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrate conversation store: %w", err)
	}
	if err := s.replay(ctx); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("replay transcripts: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	if !s.owned {
		return nil
	}
	return s.db.Close()
}

type Conversation struct {
	ID, ParentID, Role, Title, Status string
	CreatedAt, UpdatedAt              time.Time
}

type Message struct {
	ID, TurnID, Role string
	Blocks           []llmtypes.ContentBlock
	CreatedAt        time.Time
	// Seq is the message's position in its conversation, from 1. It is set
	// when a message is read back and ignored when one is appended.
	Seq int
}

func NewID(now time.Time) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var data [16]byte
	binary.BigEndian.PutUint64(data[:8], uint64(now.UnixMilli())<<16)
	rand.Read(data[6:])
	value := new(big.Int).SetBytes(data[:])
	var out [26]byte
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = alphabet[value.Uint64()&31]
		value.Rsh(value, 5)
	}
	return string(out[:])
}

const conversationColumns = "id, COALESCE(parent_id,''), role, title, status, created_at, updated_at"

type scanner interface{ Scan(...any) error }

func scanConversation(row scanner) (Conversation, error) {
	var c Conversation
	var created, updated string
	err := row.Scan(&c.ID, &c.ParentID, &c.Role, &c.Title, &c.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoConversation
	}
	if err != nil {
		return c, err
	}
	c.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return c, err
	}
	c.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return c, err
}

func (s *Store) Conversation(ctx context.Context, id string) (Conversation, error) {
	return scanConversation(s.db.Reader().QueryRowContext(ctx, "SELECT "+conversationColumns+" FROM conversations WHERE id=?", id))
}

func (s *Store) Latest(ctx context.Context) (Conversation, error) {
	return scanConversation(s.db.Reader().QueryRowContext(ctx, "SELECT "+conversationColumns+" FROM conversations ORDER BY updated_at DESC,id DESC LIMIT 1"))
}

func (s *Store) Conversations(ctx context.Context) ([]Conversation, error) {
	rows, err := s.db.Reader().QueryContext(ctx, "SELECT "+conversationColumns+" FROM conversations ORDER BY updated_at DESC,id DESC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
	out := []Conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func format(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// TurnStatus reads the projected state of a turn.
func (s *Store) TurnStatus(ctx context.Context, id string) (string, error) {
	var status string
	err := s.db.Reader().QueryRowContext(ctx, "SELECT status FROM turns WHERE id=?", id).Scan(&status)
	return status, err
}
