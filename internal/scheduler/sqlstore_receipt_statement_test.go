package scheduler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var receiptDriverSequence atomic.Uint64

type receiptCountingDriver struct {
	base       driver.Driver
	statements *atomic.Int64
}

func (d *receiptCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &receiptCountingConn{Conn: conn, statements: d.statements}, nil
}

type receiptCountingConn struct {
	driver.Conn
	statements *atomic.Int64
}

func (c *receiptCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	executor, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.statements.Add(1)
	return executor.ExecContext(ctx, query, args)
}
func (c *receiptCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	c.statements.Add(1)
	return queryer.QueryContext(ctx, query, args)
}
func (c *receiptCountingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &receiptCountingStmt{Stmt: stmt, statements: c.statements}, nil
}

type receiptCountingStmt struct {
	driver.Stmt
	statements *atomic.Int64
}

func receiptNamedValues(args []driver.Value) []driver.NamedValue {
	named := make([]driver.NamedValue, len(args))
	for i, value := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return named
}
func (s *receiptCountingStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), receiptNamedValues(args))
}
func (s *receiptCountingStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), receiptNamedValues(args))
}
func (s *receiptCountingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	executor, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, fmt.Errorf("SQLite fixture driver lacks StmtExecContext")
	}
	s.statements.Add(1)
	return executor.ExecContext(ctx, args)
}
func (s *receiptCountingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, fmt.Errorf("SQLite fixture driver lacks StmtQueryContext")
	}
	s.statements.Add(1)
	return queryer.QueryContext(ctx, args)
}

// Count actual SQLite statements, including a separately prepared pre-check.
// A check-then-insert implementation fails even if stress scheduling happens
// to hide the race. This guards the acceptance/recovery serialization boundary,
// not mutable source text. The independent-handle race test guards outcomes.
func TestSQLStoreReceiptSingleStatement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "single.db")
	a, host := newSQLAdapter(t, path, false)
	fire := sqlstoreClaimed(t, a, "single")
	var statements atomic.Int64
	name := fmt.Sprintf("scheduler_receipt_count_%d", receiptDriverSequence.Add(1))
	sql.Register(name, &receiptCountingDriver{base: host.DB.Driver(), statements: &statements})
	db, err := sql.Open(name, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	facade, err := store.NewSchedulerSQLStore(&store.Store{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if accepted, checkedErr := facade.MarkScheduleFireDispatchAccepted(ctx, fire.ID, fire.Attempt, fire.FiredAt, sqlstoreAt); checkedErr != nil || !accepted {
		t.Fatalf("accept: %v %v", accepted, checkedErr)
	}
	if n := statements.Load(); n != 1 {
		t.Fatalf("receipt admission executed %d statements; must fence and insert in one", n)
	}
	// Positive control: this connection's driver counts reads as well as writes.
	if accepted, checkedErr := facade.IsScheduleFireDispatchAccepted(ctx, fire.ID); checkedErr != nil || !accepted {
		t.Fatalf("read receipt: %v %v", accepted, checkedErr)
	}
	if n := statements.Load(); n != 2 {
		t.Fatalf("driver missed SELECT positive control: %d statements", n)
	}
	stmt, err := db.PrepareContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduler_dispatch_receipts WHERE fire_id=?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := stmt.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	var accepted bool
	if err := stmt.QueryRowContext(ctx, fire.ID).Scan(&accepted); err != nil || !accepted {
		t.Fatalf("prepared SELECT: %v %v", accepted, err)
	}
	if n := statements.Load(); n != 3 {
		t.Fatalf("driver missed prepared SELECT positive control: %d statements", n)
	}

}
