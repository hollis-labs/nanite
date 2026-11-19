package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	core "github.com/hollis-labs/libs/core/store"
)

type Job struct {
	ID              string `json:"id"`
	ConversationID  string `json:"conversation_id"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	Spec            string `json:"-"`
	ProcessIdentity string `json:"-"`
	PID             int    `json:"-"`
	OutputPath      string `json:"output_path,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
}

func (s *Store) JobDir() string { return filepath.Join(filepath.Dir(s.transcripts), "jobs") }

func (s *Store) StartJob(ctx context.Context, job Job) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO jobs(id,conversation_id,kind,status,spec_json,pid,output_path,started_at,process_identity)
  VALUES(?,?,?,'running',?,NULLIF(?,0),?,?,?)`, job.ID, job.ConversationID, job.Kind, job.Spec, job.PID, job.OutputPath, format(time.Now()), job.ProcessIdentity)
		return err
	})
}

func (s *Store) Job(ctx context.Context, conversation, kind, id string) (Job, error) {
	job := Job{ID: id, ConversationID: conversation, Kind: kind}
	var code sql.NullInt64
	err := s.db.Reader().QueryRowContext(ctx, `SELECT status,spec_json,COALESCE(pid,0),COALESCE(output_path,''),exit_code
 FROM jobs WHERE conversation_id=? AND kind=? AND id=?`, conversation, kind, id).
		Scan(&job.Status, &job.Spec, &job.PID, &job.OutputPath, &code)
	if code.Valid {
		value := int(code.Int64)
		job.ExitCode = &value
	}
	return job, err
}

func (s *Store) CompleteJob(ctx context.Context, job Job, notice Notice) error {
	return s.db.WriteTx(ctx, func(ctx context.Context, tx core.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET status=?,exit_code=?,ended_at=? WHERE id=? AND status='running'`,
			job.Status, job.ExitCode, format(time.Now()), job.ID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows == 0 {
			return err
		}
		if notice.ID == "" {
			return nil
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO notices(id,conversation_id,kind,body,created_at) VALUES(?,?,?,?,?)",
			notice.ID, notice.ConversationID, notice.Kind, notice.Body, format(time.Now()))
		return err
	})
}
