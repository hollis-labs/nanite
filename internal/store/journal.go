package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	core "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/nanite/internal/transcript"
)

type Turn struct {
	ID, UserMessageID, Status, StopReason, Error string
	Usage                                        json.RawMessage
}

type Journal struct {
	writer       *transcript.Writer
	conversation string
}

func (s *Store) OpenJournal(ctx context.Context, conversation string) (*Journal, error) {
	writer, err := transcript.Open(s.transcripts, conversation, transcript.Options{
		Project: func(entry transcript.Entry) error { return s.project(context.WithoutCancel(ctx), conversation, entry) },
	})
	if err != nil {
		return nil, err
	}
	if err := s.replayConversation(ctx, conversation); err != nil {
		_ = writer.Close()
		return nil, err
	}
	return &Journal{writer: writer, conversation: conversation}, nil
}

func (j *Journal) Close() error { return j.writer.Close() }

func (j *Journal) sideband(kind, id string, at time.Time, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	record, err := transcript.EncodeSideband(transcript.SidebandRecord{
		Type: "system", Subtype: kind, UUID: id, SessionID: j.conversation, Timestamp: at, Payload: raw,
	})
	if err != nil {
		return err
	}
	_, err = j.writer.Append(record)
	return err
}

func (s *Store) CreateConversation(ctx context.Context, c Conversation) error {
	journal, err := s.OpenJournal(ctx, c.ID)
	if err != nil {
		return err
	}
	appendErr := journal.sideband("nanite.conversation", c.ID, c.CreatedAt, c)
	return errors.Join(appendErr, journal.Close())
}

func (j *Journal) StartTurn(turn Turn, at time.Time) error {
	return j.sideband("nanite.turn.start", turn.ID, at, turn)
}

func (j *Journal) FinishTurn(turn Turn, at time.Time) error {
	return j.sideband("nanite.turn.finish", turn.ID, at, turn)
}

// InterruptTurn records the transition used when recovering an unfinished turn.
func (j *Journal) InterruptTurn(turnID string, at time.Time) error {
	return j.sideband("nanite.turn.interrupt", turnID, at, Turn{ID: turnID})
}

func (j *Journal) ResumeTurn(turn Turn, at time.Time) error {
	return j.sideband("nanite.turn.resume", turn.ID, at, turn)
}

type messageBody struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	NaniteTurnID string          `json:"nanite_turn_id"`
}

func (j *Journal) AppendMessage(message Message, parent *string) error {
	blocks, err := json.Marshal(message.Blocks)
	if err != nil {
		return err
	}
	role := message.Role
	if role == "tool" {
		role = "user"
	}
	body, err := json.Marshal(messageBody{Role: role, Content: blocks, NaniteTurnID: message.TurnID})
	if err != nil {
		return err
	}
	record, err := transcript.EncodeMessage(transcript.MessageRecord{
		Type: role, UUID: message.ID, ParentUUID: parent, SessionID: j.conversation, Timestamp: message.CreatedAt, Message: body,
	})
	if err != nil {
		return err
	}
	_, err = j.writer.Append(record)
	return err
}

func (s *Store) replay(ctx context.Context) error {
	dirs, err := os.ReadDir(s.transcripts)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		if err := s.replayConversation(ctx, dir.Name()); err != nil {
			return fmt.Errorf("conversation %s: %w", dir.Name(), err)
		}
	}
	return nil
}

func (s *Store) replayConversation(ctx context.Context, id string) error {
	var after transcript.Cursor
	var raw string
	err := s.db.Reader().QueryRowContext(ctx, "SELECT cursor FROM transcript_cursors WHERE conversation_id=?", id).Scan(&raw)
	if err == nil {
		if err := json.Unmarshal([]byte(raw), &after); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	entries, err := transcript.Replay(s.transcripts, id, after)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := s.project(ctx, id, entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) project(ctx context.Context, conversation string, entry transcript.Entry) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error { return projectEntry(ctx, tx, conversation, entry) })
}

func projectEntry(ctx context.Context, tx core.Tx, conversation string, entry transcript.Entry) error {
	var record struct {
		Type, Subtype, UUID, SessionID string
		Timestamp                      time.Time
		Message, Payload               json.RawMessage
	}
	if err := json.Unmarshal(entry.Record, &record); err != nil {
		return err
	}
	if conversation != record.SessionID {
		return fmt.Errorf("transcript conversation mismatch")
	}
	cursor, err := json.Marshal(entry.Cursor)
	if err != nil {
		return err
	}
	switch record.Subtype {
	case "nanite.conversation":
		var c Conversation
		if err := json.Unmarshal(record.Payload, &c); err != nil {
			return err
		}
		if c.ID != conversation {
			return fmt.Errorf("conversation metadata ID mismatch")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversations(id,parent_id,role,title,status,created_at,updated_at)
    VALUES(?,NULLIF(?,''),?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
			c.ID, c.ParentID, c.Role, c.Title, c.Status, format(c.CreatedAt), format(c.UpdatedAt)); err != nil {
			return err
		}
	case "nanite.compaction":
		var details Compaction
		if err := json.Unmarshal(record.Payload, &details); err != nil {
			return err
		}
		// Older development transcripts may carry compaction telemetry only.
		if details.ThroughMessageID != "" && details.Summary != "" {
			if err := projectCompaction(ctx, tx, record.UUID, conversation, details); err != nil {
				return err
			}
		}

	case "nanite.turn.start", "nanite.turn.finish", "nanite.turn.interrupt", "nanite.turn.resume":
		var turn Turn
		if err := json.Unmarshal(record.Payload, &turn); err != nil {
			return err
		}
		if err := projectTurn(ctx, tx, conversation, turn, record.Subtype, record.Timestamp); err != nil {
			return err
		}
	}
	if record.Type == "user" || record.Type == "assistant" {
		var body messageBody
		if err := json.Unmarshal(record.Message, &body); err != nil {
			return err
		}
		if err := projectMessage(ctx, tx, conversation, record.UUID, record.Timestamp, body, string(cursor)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO transcript_cursors(conversation_id,cursor) VALUES(?,?)
   ON CONFLICT(conversation_id) DO UPDATE SET cursor=excluded.cursor`, conversation, string(cursor))
	return err
}

func projectTurn(ctx context.Context, tx core.Tx, conversation string, turn Turn, subtype string, at time.Time) error {
	if subtype == "nanite.turn.start" {
		_, err := tx.ExecContext(ctx, `INSERT INTO turns(id,conversation_id,seq,status,user_message_id,started_at)
   VALUES(?,?,(SELECT COALESCE(MAX(seq),0)+1 FROM turns WHERE conversation_id=?),'running',?,?)
   ON CONFLICT(id) DO NOTHING`, turn.ID, conversation, conversation, turn.UserMessageID, format(at))
		return err
	}
	if subtype == "nanite.turn.resume" {
		_, err := tx.ExecContext(ctx, "UPDATE turns SET status='running' WHERE id=? AND conversation_id=? AND status='interrupted'", turn.ID, conversation)
		return err
	}
	if subtype == "nanite.turn.interrupt" {
		_, err := tx.ExecContext(ctx, "UPDATE turns SET status='interrupted' WHERE id=? AND conversation_id=? AND status='running'", turn.ID, conversation)
		return err
	}
	usage := string(turn.Usage)
	if usage == "" {
		usage = "{}"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE turns SET status=?,ended_at=?,stop_reason=?,usage_json=?,error=?
  WHERE id=? AND conversation_id=?`, turn.Status, format(at), turn.StopReason, usage, turn.Error, turn.ID, conversation); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE conversations SET updated_at=? WHERE id=?", format(at), conversation)
	return err
}

func projectMessage(ctx context.Context, tx core.Tx, conversation, id string, at time.Time, body messageBody, cursor string) error {
	var blocks []struct{ Type, Text string }
	if err := json.Unmarshal(body.Content, &blocks); err != nil {
		return err
	}
	role := body.Role
	if role == "user" && len(blocks) > 0 && blocks[0].Type == "tool_result" {
		role = "tool"
	}
	if role == "user" && len(blocks) > 0 {
		// An untitled conversation takes its title from the first thing the user said.
		if title := titleFrom(blocks[0].Text); title != "" {
			if _, err := tx.ExecContext(ctx, "UPDATE conversations SET title=? WHERE id=? AND title=''", title, conversation); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO messages(id,conversation_id,turn_id,seq,role,blocks_json,transcript_cursor,created_at)
  VALUES(?,?,?,(SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conversation_id=?),?,?,?,?)
  ON CONFLICT(id) DO NOTHING`, id, conversation, body.NaniteTurnID, conversation, role, string(body.Content), cursor, format(at))
	return err
}

func (s *Store) History(ctx context.Context, id string) ([]Message, error) {
	return s.historyAfter(ctx, id, "")
}

func (s *Store) historyAfter(ctx context.Context, id, after string) ([]Message, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT id,turn_id,role,blocks_json,created_at,seq FROM messages
 WHERE conversation_id=? AND seq>COALESCE((SELECT seq FROM messages WHERE conversation_id=? AND id=?),0)
 ORDER BY seq`, id, id, after)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// scanMessages reads rows of id, turn_id, role, blocks_json, created_at, seq.
func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer func() { _ = rows.Close() }() // Query errors are returned through rows.Err or Scan.
	var out []Message
	for rows.Next() {
		var m Message
		var blocks, created string
		if err := rows.Scan(&m.ID, &m.TurnID, &m.Role, &blocks, &created, &m.Seq); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(blocks), &m.Blocks); err != nil {
			return nil, err
		}
		var err error
		m.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (j *Journal) RecordPermission(id string, at time.Time, details any) error {
	return j.sideband("nanite.permission", id, at, details)
}

// titleFrom shortens text to a single line of at most 60 characters.
func titleFrom(text string) string {
	title := []rune(strings.Join(strings.Fields(text), " "))
	if len(title) > 60 {
		title = append(title[:59], '…')
	}
	return string(title)
}
