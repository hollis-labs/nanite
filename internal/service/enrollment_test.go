package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
	"github.com/hollis-labs/nanite/internal/tetherregistry"
	"github.com/hollis-labs/nanite/internal/tetherregistry/tetherregistrytest"
)

type enrollEnv struct {
	st    *store.Store
	fake  *tetherregistrytest.Fake
	svc   *EnrollmentService
	host  store.AgentHostSettings
	token string
}

func newEnrollEnv(t *testing.T) *enrollEnv {
	t.Helper()
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "enroll.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	return newEnrollEnvOn(t, st, "enroll-agent")
}

func newEnrollEnvOn(t *testing.T, st *store.Store, slug string) *enrollEnv {
	t.Helper()
	ctx := context.Background()
	def := []byte("---\nschema_version: \"2\"\ndefinition_id: def:" + slug + "\nrevision: \"1\"\nname: " + slug + "\ndescription: Enrollment fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture.\n")
	pin, err := st.InstallAgentDefinition(ctx, def, nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: slug, Title: "Enroll " + slug, DefinitionRef: pin,
		Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture", Model: "fixture-model"}, Enabled: true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	fake := tetherregistrytest.New(t)
	token := tetherregistrytest.TokenFile(t)
	return &enrollEnv{st: st, fake: fake, host: host, token: token,
		svc: &EnrollmentService{Store: st, Registrar: fake.Registrar(token)}}
}

func (e *enrollEnv) rows(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.st.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *enrollEnv) noLocalState(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{"agent_enrollments", "agent_actor_bindings", "actor_instances"} {
		if n := e.rows(t, tbl); n != 0 {
			t.Fatalf("%s has %d rows, want none", tbl, n)
		}
	}
}

func TestEnrollHappyPathAndIdempotentRetry(t *testing.T) {
	e := newEnrollEnv(t)
	ctx := context.Background()
	res, err := e.svc.Enroll(ctx, "cred-admin", e.host.ID, "needs mail", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.URN, "msg://agent/agent-mux/agt_") || res.AlreadyEnrolled || res.Status != store.DurableAgentStatusSleeping ||
		res.Binding.ActorURI != res.URN || !res.Binding.Enabled || res.InstanceID == "" {
		t.Fatalf("result = %+v", res)
	}
	installID, _ := e.st.InstallID(ctx)
	wantKey := "nanite/" + installID + "/" + e.host.ID
	if res.ExternalKey != wantKey {
		t.Fatalf("external key = %q, want %q", res.ExternalKey, wantKey)
	}
	es := e.fake.Entries()
	if len(es) != 1 || es[0].Substrate != "nanite" || es[0].ExternalID != installID+"/"+e.host.ID {
		t.Fatalf("tether entries = %+v", es)
	}
	inst, err := e.st.GetDurableAgentInstanceByURN(ctx, res.URN)
	if err != nil || inst.ID != res.InstanceID {
		t.Fatalf("instance: %v %v", inst, err)
	}

	// An enrolled agent answers from the local record: same URN, no Tether call.
	reg, _, _ := e.fake.Counts()
	again, err := e.svc.Enroll(ctx, "cred-admin", e.host.ID, "again", "")
	if err != nil || !again.AlreadyEnrolled || again.URN != res.URN || again.InstanceID != res.InstanceID {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if reg2, _, _ := e.fake.Counts(); reg2 != reg {
		t.Fatal("an already-enrolled agent called Tether again")
	}
	if e.rows(t, "agent_actor_bindings") != 1 || e.rows(t, "actor_instances") != 1 {
		t.Fatal("repeat wrote more rows")
	}
}

func TestEnrollRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("reason required", func(t *testing.T) {
		e := newEnrollEnv(t)
		for _, r := range []string{"", "  ", strings.Repeat("x", 501)} {
			if _, err := e.svc.Enroll(ctx, "c", e.host.ID, r, ""); !errors.Is(err, ErrEnrollmentReasonRequired) {
				t.Fatalf("reason %d chars: %v", len(r), err)
			}
		}
		e.noLocalState(t)
	})
	t.Run("stale revision", func(t *testing.T) {
		e := newEnrollEnv(t)
		if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", "not-the-revision"); !errors.Is(err, ErrEnrollmentStaleRevision) {
			t.Fatal(err)
		}
		e.noLocalState(t)
		if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", e.host.Revision); err != nil {
			t.Fatalf("current revision refused: %v", err)
		}
	})
	t.Run("unknown host", func(t *testing.T) {
		e := newEnrollEnv(t)
		if _, err := e.svc.Enroll(ctx, "c", "no-such", "r", ""); err == nil {
			t.Fatal("accepted")
		}
		e.noLocalState(t)
	})
	t.Run("tether not configured writes nothing", func(t *testing.T) {
		e := newEnrollEnv(t)
		e.svc.Registrar = &tetherregistry.Client{Config: func() tetherregistry.Config { return tetherregistry.Config{} }}
		if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.Is(err, tetherregistry.ErrNotConfigured) {
			t.Fatal(err)
		}
		e.noLocalState(t)
	})
	t.Run("registry token missing writes nothing", func(t *testing.T) {
		e := newEnrollEnv(t)
		e.svc.Registrar = e.fake.Registrar(filepath.Join(t.TempDir(), "absent"))
		if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.Is(err, tetherregistry.ErrTokenMissing) {
			t.Fatal(err)
		}
		e.noLocalState(t)
		if r, l, d := e.fake.Counts(); r+l+d != 0 {
			t.Fatal("Tether was called without a token")
		}
	})
	t.Run("registry token with group or world access writes nothing", func(t *testing.T) {
		e := newEnrollEnv(t)
		if err := os.Chmod(e.token, 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.Is(err, tetherregistry.ErrTokenInsecure) {
			t.Fatal(err)
		}
		e.noLocalState(t)
		if r, _, _ := e.fake.Counts(); r != 0 {
			t.Fatal("Tether was called with an insecure token file")
		}
	})
	t.Run("the bridge token is not used", func(t *testing.T) {
		t.Setenv("NANITE_TETHER_TOKEN_FILE", e0Token(t))
		t.Setenv("NANITE_TETHER_ADDR", "http://127.0.0.1:1")
		t.Setenv(tetherregistry.TokenFileEnv, "")
		if err := (&tetherregistry.Client{}).Ready(); !errors.Is(err, tetherregistry.ErrTokenMissing) {
			t.Fatalf("bridge token file satisfied the registry credential: %v", err)
		}
	})
}

func e0Token(t *testing.T) string {
	t.Helper()
	return tetherregistrytest.TokenFile(t)
}

func TestEnrollTetherFailuresAreTypedAndRetryable(t *testing.T) {
	ctx := context.Background()
	e := newEnrollEnv(t)

	e.fake.Drop()
	if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.Is(err, tetherregistry.ErrUnreachable) {
		t.Fatalf("down: %v", err)
	}
	// The record survives; no actor yet.
	rec, err := e.st.ActorEnrollment().Get(ctx, e.host.ID)
	if err != nil || rec.Status != store.EnrollmentEnrolling || rec.LastError != "tether_unreachable" || rec.Attempts != 1 {
		t.Fatalf("record = %+v %v", rec, err)
	}
	if e.rows(t, "agent_actor_bindings") != 0 || e.rows(t, "actor_instances") != 0 {
		t.Fatal("actor rows written while Tether was down")
	}

	var ref *tetherregistry.RefusedError
	e.fake.FailWith(400, "invalid_request")
	if _, err = e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.As(err, &ref) || ref.Status != 400 || ref.Code != "invalid_request" {
		t.Fatalf("400: %v", err)
	}
	for _, code := range []int{401, 403} {
		e.fake.FailWith(code, "forbidden")
		if _, err = e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.As(err, &ref) || !ref.Unauthorized() {
			t.Fatalf("%d: %v", code, err)
		}
	}
	e.fake.Slow(300 * time.Millisecond)
	slow := e.fake.Registrar(e.token)
	slow.HTTP.Timeout = 50 * time.Millisecond
	e.svc.Registrar = slow
	if _, err = e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.Is(err, tetherregistry.ErrUnreachable) {
		t.Fatalf("timeout: %v", err)
	}

	e.fake.Heal()
	e.svc.Registrar = e.fake.Registrar(e.token)
	res, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", "")
	if err != nil || res.AlreadyEnrolled {
		t.Fatalf("recovery: %+v %v", res, err)
	}
	if rec, _ = e.st.ActorEnrollment().Get(ctx, e.host.ID); rec.Status != store.EnrollmentEnrolled || rec.LastError != "" {
		t.Fatalf("record after recovery = %+v", rec)
	}
}

func TestEnrollCrashBetweenTetherAndLocalCommitIsRecoverable(t *testing.T) {
	ctx := context.Background()
	e := newEnrollEnv(t)
	// Simulate the crash: Tether succeeds, the local writer fails once.
	if _, err := e.st.DB.ExecContext(ctx, `CREATE TRIGGER inject_fail BEFORE INSERT ON actor_instances BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); err == nil {
		t.Fatal("enroll succeeded despite the local failure")
	}
	es := e.fake.Entries()
	if len(es) != 1 {
		t.Fatalf("tether entries = %d, want the registration to have happened", len(es))
	}
	firstURN := es[0].URN
	if e.rows(t, "agent_actor_bindings") != 0 || e.rows(t, "actor_instances") != 0 {
		t.Fatal("partial local state after the failed writer")
	}
	rec, _ := e.st.ActorEnrollment().Get(ctx, e.host.ID)
	if rec.Status != store.EnrollmentEnrolling || rec.LastError != "local_write_failed" {
		t.Fatalf("record = %+v", rec)
	}

	if _, err := e.st.DB.ExecContext(ctx, `DROP TRIGGER inject_fail`); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", "")
	if err != nil || res.URN != firstURN {
		t.Fatalf("retry: %+v %v (want the same URN %s)", res, err, firstURN)
	}
	if len(e.fake.Entries()) != 1 {
		t.Fatal("the retry created a second Tether entry")
	}
	if e.rows(t, "agent_actor_bindings") != 1 || e.rows(t, "actor_instances") != 1 {
		t.Fatal("retry did not complete the local write")
	}
}

func TestConcurrentEnrollOfOneAgentConverges(t *testing.T) {
	ctx := context.Background()
	e := newEnrollEnv(t)
	const n = 8
	var wg sync.WaitGroup
	urns := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", "")
			urns[i], errs[i] = res.URN, err
		}(i)
	}
	wg.Wait()
	for i := range urns {
		if errs[i] != nil || urns[i] != urns[0] || urns[i] == "" {
			t.Fatalf("caller %d: %q %v (first %q)", i, urns[i], errs[i], urns[0])
		}
	}
	if len(e.fake.Entries()) != 1 || e.rows(t, "agent_actor_bindings") != 1 || e.rows(t, "actor_instances") != 1 || e.rows(t, "agent_enrollments") != 1 {
		t.Fatal("concurrent enrolls produced duplicate rows or entries")
	}
}

func TestRetireWithTetherDownIsPendingThenRetried(t *testing.T) {
	ctx := context.Background()
	e := newEnrollEnv(t)
	res, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", "")
	if err != nil {
		t.Fatal(err)
	}
	e.fake.Drop()
	out, err := e.svc.Retire(ctx, "c", e.host.ID, "done")
	if err != nil {
		t.Fatalf("retire must succeed locally with Tether down: %v", err)
	}
	if !out.Enrolled || !out.DeregisterPending || out.Status != store.EnrollmentDeregisterPending || out.DeregisterError != "tether_unreachable" || out.URN != res.URN {
		t.Fatalf("retire = %+v", out)
	}
	inst, _ := e.st.GetDurableAgentInstanceByURN(ctx, res.URN)
	if inst.Status != store.DurableAgentStatusArchived || inst.CurrentSessionID != "" {
		t.Fatalf("instance still live: %+v", inst)
	}
	if es := e.fake.Entries(); es[0].Status != "active" {
		t.Fatal("deregistration reached a Tether that was down")
	}
	if ok, _ := e.st.AttachActorSession(ctx, e.host.ID, "s"); ok {
		t.Fatal("retired actor is still routable")
	}

	// A later retire retries the pending deregistration.
	e.fake.Heal()
	out, err = e.svc.Retire(ctx, "c", e.host.ID, "retry")
	if err != nil || out.DeregisterPending || out.Status != store.EnrollmentRetired {
		t.Fatalf("retry = %+v %v", out, err)
	}
	if es := e.fake.Entries(); es[0].Status != "deprecated" {
		t.Fatalf("tether entry = %+v", es[0])
	}
	if _, err = e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); !errors.Is(err, store.ErrEnrollmentRetired) {
		t.Fatalf("re-enrolling a retired actor: %v", err)
	}
}

func TestRetirePendingIsAlsoRetriedByAnotherAgentsRetire(t *testing.T) {
	ctx := context.Background()
	a := newEnrollEnv(t)
	b := newEnrollEnvOn(t, a.st, "enroll-agent-b")
	b.fake, b.svc = a.fake, &EnrollmentService{Store: a.st, Registrar: a.fake.Registrar(a.token)}
	for _, h := range []string{a.host.ID, b.host.ID} {
		if _, err := a.svc.Enroll(ctx, "c", h, "r", ""); err != nil {
			t.Fatal(err)
		}
	}
	a.fake.Drop()
	_, _ = a.svc.Retire(ctx, "c", a.host.ID, "r")
	a.fake.Heal()
	if _, err := a.svc.Retire(ctx, "c", b.host.ID, "r"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{a.host.ID, b.host.ID} {
		if rec, _ := a.st.ActorEnrollment().Get(ctx, h); rec.Status != store.EnrollmentRetired {
			t.Fatalf("%s status = %s", h, rec.Status)
		}
	}
}

func TestRetireWithoutConfigStaysPendingAndRetireOfUnenrolledIsANoop(t *testing.T) {
	ctx := context.Background()
	e := newEnrollEnv(t)
	if out, err := e.svc.Retire(ctx, "c", e.host.ID, "r"); err != nil || out.Enrolled {
		t.Fatalf("retire of an agent never enrolled: %+v %v", out, err)
	}
	if _, err := e.svc.Enroll(ctx, "c", e.host.ID, "r", ""); err != nil {
		t.Fatal(err)
	}
	e.svc.Registrar = &tetherregistry.Client{Config: func() tetherregistry.Config { return tetherregistry.Config{} }}
	out, err := e.svc.Retire(ctx, "c", e.host.ID, "r")
	if err != nil || !out.DeregisterPending || out.DeregisterError != "tether_not_configured" {
		t.Fatalf("retire without config: %+v %v", out, err)
	}
}

func TestRetireOfAnUncompletedEnrollmentLooksTheKeyUp(t *testing.T) {
	ctx := context.Background()
	e := newEnrollEnv(t)
	// Crash window: registered in Tether, local write never happened.
	if _, err := e.st.DB.ExecContext(ctx, `CREATE TRIGGER inject_fail BEFORE INSERT ON actor_instances BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.Enroll(ctx, "c", e.host.ID, "r", "")
	_, _ = e.st.DB.ExecContext(ctx, `DROP TRIGGER inject_fail`)
	out, err := e.svc.Retire(ctx, "c", e.host.ID, "abandon")
	if err != nil || out.DeregisterPending || out.Status != store.EnrollmentRetired {
		t.Fatalf("retire = %+v %v", out, err)
	}
	if es := e.fake.Entries(); len(es) != 1 || es[0].Status != "deprecated" {
		t.Fatalf("the orphaned Tether entry was not deregistered: %+v", es)
	}
}

func TestSessionStartStopSetsAndClearsCurrentSession(t *testing.T) {
	ctx := context.Background()
	e := newEnrollEnv(t)
	res, _ := e.svc.Enroll(ctx, "c", e.host.ID, "r", "")
	e.svc.AttachSession(ctx, e.host.ID, "sess-a")
	inst, _ := e.st.GetDurableAgentInstanceByURN(ctx, res.URN)
	if inst.CurrentSessionID != "sess-a" {
		t.Fatalf("current session = %q", inst.CurrentSessionID)
	}
	e.svc.DetachSession(ctx, "sess-b") // not the attached session
	if inst, _ = e.st.GetDurableAgentInstanceByURN(ctx, res.URN); inst.CurrentSessionID != "sess-a" {
		t.Fatal("an unrelated session end cleared the actor")
	}
	e.svc.DetachSession(ctx, "sess-a")
	if inst, _ = e.st.GetDurableAgentInstanceByURN(ctx, res.URN); inst.CurrentSessionID != "" {
		t.Fatalf("current session after stop = %q", inst.CurrentSessionID)
	}
}

// The enrollment writer has one consumer: this service. Go cannot hide a
// method, so this pins the reviewed set; a second consumer is a deliberate,
// reviewed change.
func TestActorEnrollmentWriterHasOneConsumer(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\.ActorEnrollment\(\)`)
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "ui":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		checked++
		if re.Match(data) && rel != "internal/service/enrollment.go" && rel != "internal/store/enrollment.go" {
			t.Errorf("%s calls Store.ActorEnrollment(); only the enrollment service may", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("scanned only %d Go files", checked)
	}
}
