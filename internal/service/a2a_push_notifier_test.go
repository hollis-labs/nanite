package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/interception/egress"
)

func allowLocalHTTPWebhookTest(notifier *A2APushNotifier) {
	notifier.allowHTTP = true
	notifier.allowLocalhost = true
}

func TestA2APushNotifierRequiresHTTPSByDefault(t *testing.T) {
	notifier := NewA2APushNotifier(nil, nil)
	dialed := false
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}

	req, err := http.NewRequest(http.MethodPost, "http://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); err == nil || !strings.Contains(err.Error(), "HTTPS required") {
		t.Fatalf("HTTP webhook error = %v, want HTTPS-required rejection", err)
	}
	if dialed {
		t.Fatal("HTTP webhook reached the dialer before scheme rejection")
	}
}

func TestA2APushNotifierBlocksSharedSSRFPolicyRanges(t *testing.T) {
	tests := map[string]string{
		"rfc1918-10":       "10.1.2.3",
		"rfc1918-172":      "172.16.1.2",
		"rfc1918-192":      "192.168.1.2",
		"ipv4-loopback":    "127.0.0.1",
		"ipv6-loopback":    "::1",
		"imds-link-local":  "169.254.169.254",
		"ipv6-link-local":  "fe80::1",
		"cgnat":            "100.64.0.1",
		"ipv6-ula":         "fd00::1",
		"ipv4-unspecified": "0.0.0.0",
		"ipv6-unspecified": "::",
	}
	for name, blocked := range tests {
		t.Run(name, func(t *testing.T) {
			notifier := NewA2APushNotifier(nil, nil)
			dialed := false
			notifier.resolver = func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP(blocked)}, nil
			}
			notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			}

			req, err := http.NewRequest(http.MethodPost, "https://callback.example/hook", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, egress.ErrSSRFBlocked) {
				t.Fatalf("blocked IP %s error = %v, want egress.ErrSSRFBlocked", blocked, err)
			}
			if dialed {
				t.Fatalf("blocked IP %s reached the dialer", blocked)
			}
		})
	}
}

func TestA2APushNotifierRejectsMixedDNSAnswers(t *testing.T) {
	notifier := NewA2APushNotifier(nil, nil)
	dialed := false
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("169.254.169.254")}, nil
	}
	notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}

	req, err := http.NewRequest(http.MethodPost, "https://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, egress.ErrSSRFBlocked) {
		t.Fatalf("mixed DNS answer error = %v, want egress.ErrSSRFBlocked", err)
	}
	if dialed {
		t.Fatal("mixed DNS answer reached the dialer")
	}
}

func TestA2APushNotifierDialsPinnedLiteral(t *testing.T) {
	notifier := NewA2APushNotifier(nil, nil)
	var dialAddr string
	dialErr := errors.New("stop after pinned dial")
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.25")}, nil
	}
	notifier.dialer = func(_ context.Context, _ string, addr string) (net.Conn, error) {
		dialAddr = addr
		return nil, dialErr
	}

	req, err := http.NewRequest(http.MethodPost, "https://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, dialErr) {
		t.Fatalf("pinned dial error = %v, want sentinel", err)
	}
	if dialAddr != "203.0.113.25:443" {
		t.Fatalf("dial address = %q, want pinned literal", dialAddr)
	}
}

func TestA2APushNotifierRevalidatesRedirectDNS(t *testing.T) {
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer redirectServer.Close()

	notifier := NewA2APushNotifier(nil, nil)
	notifier.allowHTTP = true
	resolveCalls := 0
	notifier.resolver = func(context.Context, string) ([]net.IP, error) {
		resolveCalls++
		if resolveCalls == 1 {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	dialCalls := 0
	notifier.dialer = func(ctx context.Context, network, _ string) (net.Conn, error) {
		dialCalls++
		return (&net.Dialer{}).DialContext(ctx, network, redirectServer.Listener.Addr().String())
	}

	req, err := http.NewRequest(http.MethodPost, "http://callback.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notifier.secureHTTPClient().Do(req); !errors.Is(err, egress.ErrSSRFBlocked) {
		t.Fatalf("redirect rebind error = %v, want egress.ErrSSRFBlocked", err)
	}
	if resolveCalls != 2 {
		t.Fatalf("resolver calls = %d, want initial request plus redirect", resolveCalls)
	}
	if dialCalls != 1 {
		t.Fatalf("inner dial calls = %d, want only the validated initial request", dialCalls)
	}
}

// These fixtures are retained historical records in a private database. They
// bypass retired writers only to prove that history is neither deleted nor
// promoted into operational authority. They do not supply an issuer or actor.
func historicalA2ATask(t *testing.T, st *store.Store, task *store.A2ATask) {
	t.Helper()
	var run, config any
	if task.WorkflowRunID.Valid {
		run = task.WorkflowRunID.String
	}
	if task.PushNotificationConfig.Valid {
		config = task.PushNotificationConfig.String
	}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO a2a_tasks(id,target_kind,target_ref,message,workflow_run_id,state,push_notification_config) VALUES(?,?,?,?,?,?,?)`, task.ID, task.TargetKind, task.TargetRef, task.Message, run, task.State, config); err != nil {
		t.Fatal(err)
	}
}

func historicalA2APush(t *testing.T, st *store.Store, taskID string, attempts int) *store.A2APushDelivery {
	t.Helper()
	row := &store.A2APushDelivery{ID: "historical-delivery", TaskID: taskID, TargetState: "working", AttemptCount: attempts, LastError: "retained private delivery error", NextRetry: time.Now().Add(-time.Minute)}
	if _, err := st.DB.ExecContext(t.Context(), `INSERT INTO a2a_push_deliveries(id,task_id,target_state,attempt_count,last_error,next_retry) VALUES(?,?,?,?,?,?)`, row.ID, row.TaskID, row.TargetState, row.AttemptCount, row.LastError, row.NextRetry); err != nil {
		t.Fatal(err)
	}
	return row
}

func a2aRefusalState(t *testing.T, st *store.Store) map[string][][]any {
	t.Helper()
	tables, err := st.DB.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND (name LIKE 'a2a_%' OR name LIKE 'agent_%' OR name LIKE 'actor_%' OR name LIKE 'workflow_%' OR name='session_actor_bindings') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if scanErr := tables.Scan(&name); scanErr != nil {
			t.Fatal(scanErr)
		}
		names = append(names, name)
	}
	if rowsErr := tables.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if closeErr := tables.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	state := make(map[string][][]any, len(names))
	for _, name := range names {
		rows, queryErr := st.DB.QueryContext(t.Context(), fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, name))
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		columns, columnErr := rows.Columns()
		if columnErr != nil {
			t.Fatal(columnErr)
		}
		state[name] = make([][]any, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if scanErr := rows.Scan(targets...); scanErr != nil {
				t.Fatal(scanErr)
			}
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					values[i] = string(raw)
				}
			}
			state[name] = append(state[name], values)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			t.Fatal(rowsErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	for _, required := range []string{"a2a_tasks", "a2a_push_deliveries", "agent_definitions", "agent_host_settings", "agent_actor_bindings"} {
		if _, ok := state[required]; !ok {
			t.Fatalf("missing refusal fixture table %s", required)
		}
	}
	return state
}

func assertA2ARefusalState(t *testing.T, st *store.Store, before map[string][][]any) {
	t.Helper()
	after := a2aRefusalState(t, st)
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Fatalf("held operation changed %s rows", table)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("held operation changed graph tables")
	}
}

func TestA2ATaskPersistenceRefusesWithoutVerifiedAuthority(t *testing.T) {
	st := newTestStore(t)
	defer st.Close(context.Background())
	historical := &store.A2ATask{ID: "historical-task", TargetKind: "workflow", TargetRef: "historical-workflow", Message: "retained private message", State: a2a.TaskStateWorking}
	historicalA2ATask(t, st, historical)
	before := a2aRefusalState(t, st)
	for _, kind := range []string{"workflow", "instance"} {
		request := &store.A2ATask{ID: "claimed-task", TargetKind: kind, TargetRef: "msg://agent/claimed/target", Message: "claimed", State: a2a.TaskStateSubmitted}
		if err := st.CreateA2ATask(t.Context(), request); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Fatalf("claimed task create = %v", err)
		}
		assertA2ARefusalState(t, st, before)
	}
	if task, err := st.GetA2ATask(t.Context(), historical.ID); task != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("history became operational: %+v %v", task, err)
	}
	historical.State = a2a.TaskStateCompleted
	if err := st.UpdateA2ATask(t.Context(), historical); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("historical update = %v", err)
	}
	assertA2ARefusalState(t, st, before)
}

func TestA2APushPersistenceRefusesAndPreservesHistory(t *testing.T) {
	st := newTestStore(t)
	defer st.Close(context.Background())
	task := &store.A2ATask{ID: "historical-task", TargetKind: "workflow", TargetRef: "historical-workflow", Message: "retained", State: a2a.TaskStateWorking}
	historicalA2ATask(t, st, task)
	delivery := historicalA2APush(t, st, task.ID, 2)
	before := a2aRefusalState(t, st)
	if err := st.CreateA2APushDelivery(t.Context(), &store.A2APushDelivery{ID: "new-delivery", TaskID: task.ID, TargetState: "completed"}); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("delivery create = %v", err)
	}
	if pending, err := st.GetPendingPushDeliveries(t.Context(), time.Now()); pending != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("historical delivery exposed: %+v %v", pending, err)
	}
	delivery.AttemptCount = 3
	if err := st.UpdateA2APushDelivery(t.Context(), delivery); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("retry update = %v", err)
	}
	if err := st.DeleteA2APushDelivery(t.Context(), delivery.ID); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("history delete = %v", err)
	}
	assertA2ARefusalState(t, st, before)
}

func TestA2APushNotifierHeldPathsDoNotDialOrRetryHistory(t *testing.T) {
	for _, attempts := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(attempts), func(t *testing.T) {
			st := newTestStore(t)
			defer st.Close(context.Background())
			task := &store.A2ATask{ID: "historical-task", TargetKind: "workflow", TargetRef: "historical-workflow", Message: "retained", State: a2a.TaskStateWorking, PushNotificationConfig: sql.NullString{String: `{"url":"https://callback.example/hook","token":"private-history-token"}`, Valid: true}}
			historicalA2ATask(t, st, task)
			delivery := historicalA2APush(t, st, task.ID, attempts)
			before := a2aRefusalState(t, st)
			notifier := NewA2APushNotifier(st, nil)
			notifier.resolver = func(context.Context, string) ([]net.IP, error) {
				t.Error("held delivery reached DNS")
				return nil, errors.New("unexpected DNS")
			}
			notifier.dialer = func(context.Context, string, string) (net.Conn, error) {
				t.Error("held delivery reached dialer")
				return nil, errors.New("unexpected dial")
			}
			if err := notifier.EnqueueDelivery(task.ID, a2a.TaskStateCompleted); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("enqueue = %v", err)
			}
			if err := notifier.ProcessPendingDeliveries(t.Context()); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("ticker process = %v", err)
			}
			if err := notifier.processDelivery(t.Context(), delivery); !errors.Is(err, store.ErrVerifiedActorRequired) {
				t.Fatalf("direct historical delivery = %v", err)
			}
			assertA2ARefusalState(t, st, before)
		})
	}
}

func TestA2APushTickerCannotCancelOrDeliverHistoricalTask(t *testing.T) {
	st := newTestStore(t)
	defer st.Close(context.Background())
	task := &store.A2ATask{ID: "historical-task", TargetKind: "instance", TargetRef: "msg://agent/claimed/old-instance", Message: "retained", State: a2a.TaskStateWorking, PushNotificationConfig: sql.NullString{String: `{"url":"https://callback.example/hook"}`, Valid: true}}
	historicalA2ATask(t, st, task)
	historicalA2APush(t, st, task.ID, 0)
	before := a2aRefusalState(t, st)
	tm := &TaskManager{store: st, logger: testLogger(t), pushNotifier: NewA2APushNotifier(st, testLogger(t))}
	if result, err := tm.CancelTask(t.Context(), task.ID); result != nil || !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("CancelTask = %+v %v", result, err)
	}
	if err := tm.PushNotifier().ProcessPendingDeliveries(t.Context()); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("ticker process = %v", err)
	}
	assertA2ARefusalState(t, st, before)
}
