package main

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeService is the "service": a tiny HTTP server whose reported version is whatever
// the binary behind the symlink contains at (fake) restart time.
type fakeService struct {
	srv      *httptest.Server
	mu       sync.Mutex
	version  string
	status   int
	failures int // next N restarts come up unhealthy (503)
	restarts int32
}

func newService(t *testing.T) *fakeService {
	s := &fakeService{status: 200}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		v, st := s.version, s.status
		s.mu.Unlock()
		w.WriteHeader(st)
		fmt.Fprintf(w, `{"status":"ok","version":%q}`, v)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// restart simulates a process restart: it reads the binary through the symlink.
func (s *fakeService) restart(link string) error {
	b, err := os.ReadFile(link)
	if err != nil {
		return err
	}
	atomic.AddInt32(&s.restarts, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = strings.TrimSpace(string(b))
	s.status = 200
	if s.failures > 0 {
		s.failures--
		s.status = 503
	}
	return nil
}

type env struct {
	t           *testing.T
	d           *Deps
	fr          *fakeRunner
	out         interface{ String() string }
	state       string
	installRoot string
	link        string
	svc         *fakeService
	spec        string
}

func newEnv(t *testing.T, restart, preflight string) *env {
	d, fr, out := newDeps(t)
	e := &env{t: t, d: d, fr: fr, out: out, state: t.TempDir(), svc: newService(t)}
	base := t.TempDir()
	e.installRoot = filepath.Join(base, "releases")
	e.link = filepath.Join(base, "bin", "nanite")
	if restart == "" {
		restart = `      - {via: command, argv: [fake-restart]}`
	}
	e.spec = specFor(specOpts{Root: e.installRoot, Link: e.link, Restart: restart, Preflight: preflight,
		Health: fmt.Sprintf(`      - {url: "%s/health", expect_status: [200], timeout: 300ms, interval: 20ms, version_json: version}`, e.svc.srv.URL)})
	fr.real = true
	fr.handler = func(name string, args []string) (string, error, bool) {
		if name == "fake-restart" {
			return "", e.svc.restart(e.link), true
		}
		return "", nil, false
	}
	return e
}

func (e *env) build(version string) artifact {
	return buildArtifact(e.t, buildOpts{Version: version, Binary: version + "\n", Spec: e.spec})
}

func (e *env) deploy(a artifact, mod func(*DeployOpts)) error {
	o := DeployOpts{App: "nanite", Source: Source{FromFile: a.Tarball}, Root: e.state}
	if mod != nil {
		mod(&o)
	}
	return e.d.Deploy(context.Background(), o)
}

func (e *env) linkContent() string {
	b, _ := os.ReadFile(e.link)
	return strings.TrimSpace(string(b))
}

func TestFromFileEndToEnd(t *testing.T) {
	e := newEnv(t, "", "")
	a := e.build("1.0.0-g1111111")
	if err := e.deploy(a, nil); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	bin := filepath.Join(e.installRoot, "1.0.0-g1111111", "nanite")
	fi, err := os.Stat(bin)
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary: %v %v", fi, err)
	}
	if tg, _ := os.Readlink(e.link); tg != bin {
		t.Fatalf("symlink -> %q", tg)
	}
	for _, f := range []string{"deploy.yaml", "build-info.json"} {
		if !fileExists(filepath.Join(e.installRoot, "1.0.0-g1111111", f)) {
			t.Errorf("%s not kept", f)
		}
	}
	st, _ := LoadState(e.installRoot)
	if st.Current.Version != "1.0.0-g1111111" || st.Previous != nil {
		t.Fatalf("state %+v", st)
	}
	if err := e.d.Status(context.Background(), "nanite", "", e.state); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := e.d.List(e.state); err != nil {
		t.Fatal(err)
	}
	mustContain(t, e.out.String(), "installed: 1.0.0-g1111111", "health: ok", "nanite\tt1\t1.0.0-g1111111")
}

func TestChecksumRefusals(t *testing.T) {
	e := newEnv(t, "", "")
	a := e.build("1.0.0")
	os.WriteFile(a.Tarball, append([]byte{}, 'x'), 0o644)
	err := e.deploy(a, nil)
	if exitCode(err) != ExitRefused || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("mismatch: %v", err)
	}
	b := e.build("2.0.0")
	os.WriteFile(b.Sums, []byte(strings.Repeat("0", 64)+"  other.tar.gz\n"), 0o644)
	err = e.deploy(b, nil)
	if exitCode(err) != ExitRefused || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("missing entry: %v", err)
	}
	c := buildArtifact(t, buildOpts{Version: "3.0.0", Binary: "3", Spec: e.spec, NoBuildInfoInSums: true})
	if err := e.deploy(c, nil); exitCode(err) != ExitRefused {
		t.Fatalf("unsummed build-info: %v", err)
	}
	if fileExists(e.link) || fileExists(filepath.Join(e.installRoot, "1.0.0")) {
		t.Fatal("something was installed")
	}
}

func TestTraversalTarballRefusedAtDeploy(t *testing.T) {
	e := newEnv(t, "", "")
	a := buildArtifact(t, buildOpts{Version: "1.0.0", Binary: "1", Spec: e.spec, Extra: func(tw *tar.Writer) {
		tw.WriteHeader(&tar.Header{Name: "../../escape", Mode: 0o644, Size: 1, Typeflag: '0'})
		tw.Write([]byte("x"))
	}})
	if err := e.deploy(a, nil); exitCode(err) != ExitRefused || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("%v", err)
	}
}

func TestSQLZeroBlocksAndForce(t *testing.T) {
	needSqlite(t)
	db := filepath.Join(t.TempDir(), "main.db")
	mustSqlite(t, db, "create table agent_runtime(state text)", "insert into agent_runtime values('running')")
	pf := fmt.Sprintf(`      - {kind: sql_zero, db: %s, sql: "select count(*) from agent_runtime where state in ('launching','running')"}`, db)
	e := newEnv(t, "", pf)
	a := e.build("1.0.0")
	err := e.deploy(a, nil)
	if exitCode(err) != ExitRefused || !strings.Contains(err.Error(), "active work") {
		t.Fatalf("%v", err)
	}
	if fileExists(e.link) {
		t.Fatal("installed despite active work")
	}
	if err := e.deploy(a, func(o *DeployOpts) { o.Force = true }); err != nil {
		t.Fatalf("--force: %v\n%s", err, e.out.String())
	}
	mustContain(t, e.out.String(), "continuing because of --force")
	// zero rows: passes
	mustSqlite(t, db, "delete from agent_runtime")
	e2 := newEnv(t, "", pf)
	if err := e2.deploy(e2.build("1.0.0"), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteBackup(t *testing.T) {
	needSqlite(t)
	db := filepath.Join(t.TempDir(), "main.db")
	mustSqlite(t, db, "pragma journal_mode=wal", "create table t(x)", "insert into t values(42)")
	pf := fmt.Sprintf(`      - {kind: sqlite_backup, db: %s, dest: "{backup_dir}/main.pre-{version}-{sha12}-{app}.db"}`, db)
	e := newEnv(t, "", pf)
	if err := e.deploy(e.build("1.0.0"), nil); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	st, _ := LoadState(e.installRoot)
	dir := st.Current.BackupDir
	dest := filepath.Join(dir, "main.pre-1.0.0-abababababab-nanite.db")
	if fi, err := os.Stat(dest); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup file: %v %v", fi, err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("backup dir mode %v", fi.Mode().Perm())
	}
	out, _ := e.d.sqlite(context.Background(), dest, "select x from t")
	if out != "42" {
		t.Fatalf("backup content %q", out)
	}
}

func TestSQLiteBackupIntegrityFailure(t *testing.T) {
	needSqlite(t)
	db := filepath.Join(t.TempDir(), "main.db")
	mustSqlite(t, db, "create table t(x)")
	pf := fmt.Sprintf(`      - {kind: sqlite_backup, db: %s, dest: "{backup_dir}/b.db"}`, db)
	e := newEnv(t, "", pf)
	inner := e.fr.handler
	e.fr.handler = func(name string, args []string) (string, error, bool) {
		if name == "sqlite3" && len(args) == 2 && args[1] == "pragma integrity_check" {
			return "*** in database main ***\nPage 2: corrupt", nil, true
		}
		return inner(name, args)
	}
	err := e.deploy(e.build("1.0.0"), nil)
	if exitCode(err) != ExitRefused || !strings.Contains(err.Error(), "integrity_check") {
		t.Fatalf("%v", err)
	}
	if fileExists(e.link) {
		t.Fatal("installed after failed backup")
	}
}

func TestMigrationCheck(t *testing.T) {
	needSqlite(t)
	db := filepath.Join(t.TempDir(), "main.db")
	mustSqlite(t, db, "create table goose_db_version(version_id integer)", "insert into goose_db_version values(5)")
	pf := fmt.Sprintf(`      - {kind: migration_check, db: %s}`, db)
	mk := func(migs int) (*env, artifact) {
		e := newEnv(t, "", pf)
		return e, buildArtifact(t, buildOpts{Version: "1.0.0", Binary: "1.0.0\n", Spec: e.spec, Migrations: migs})
	}
	e, a := mk(7)
	err := e.deploy(a, nil)
	if exitCode(err) != ExitRefused || !strings.Contains(err.Error(), "5 -> 7") || !strings.Contains(err.Error(), "--accept-migration") {
		t.Fatalf("%v", err)
	}
	mustContain(t, e.out.String(), "database goose version 5, build migrations_max 7")
	if fileExists(e.link) {
		t.Fatal("installed")
	}
	if err := e.deploy(a, func(o *DeployOpts) { o.AcceptMigration = true }); err != nil {
		t.Fatalf("accept: %v", err)
	}
	e, a = mk(5)
	if err := e.deploy(a, nil); err != nil {
		t.Fatalf("equal versions: %v", err)
	}
}

func TestAtomicSymlinkFlip(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "bin", "app")
	if err := flipLink(link, "/a"); err != nil {
		t.Fatal(err)
	}
	var bad int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tg, err := os.Readlink(link)
				// macOS/APFS can answer EINVAL for a moment while a symlink is renamed over
				// another (verified with a bare rename loop); the link is never missing.
				if runtime.GOOS == "darwin" && errors.Is(err, syscall.EINVAL) {
					continue
				}
				if err != nil || (tg != "/a" && tg != "/b") {
					atomic.AddInt32(&bad, 1)
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		if err := flipLink(link, []string{"/b", "/a"}[i%2]); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad != 0 {
		t.Fatalf("reader saw a missing/odd link %d times", bad)
	}
}

func TestExistingRegularFileBecomesLegacyRollbackPoint(t *testing.T) {
	e := newEnv(t, "", "")
	os.MkdirAll(filepath.Dir(e.link), 0o755)
	os.WriteFile(e.link, []byte("legacy-binary\n"), 0o755)
	if err := e.deploy(e.build("1.0.0"), nil); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	st, _ := LoadState(e.installRoot)
	if st.Previous == nil || st.Previous.Version != "legacy" {
		t.Fatalf("previous %+v", st.Previous)
	}
	b, _ := os.ReadFile(e.link + ".pre-fleet-install")
	if strings.TrimSpace(string(b)) != "legacy-binary" {
		t.Fatal("legacy copy missing")
	}
	// A manual rollback goes back to the legacy file; the service reports it without a version check.
	e.svc.mu.Lock()
	e.svc.version = "legacy-binary"
	e.svc.mu.Unlock()
	if err := e.d.Rollback(context.Background(), "nanite", "", e.state, false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if e.linkContent() != "legacy-binary" {
		t.Fatal("legacy not restored")
	}
}

func TestPreviousBookkeepingAndPruneTo3(t *testing.T) {
	e := newEnv(t, "", "")
	vs := []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0"}
	for _, v := range vs {
		if err := e.deploy(e.build(v), nil); err != nil {
			t.Fatalf("%s: %v\n%s", v, err, e.out.String())
		}
	}
	st, _ := LoadState(e.installRoot)
	if st.Current.Version != "1.4.0" || st.Previous.Version != "1.3.0" || len(st.History) != 3 {
		t.Fatalf("state: cur %s prev %v history %d", st.Current.Version, st.Previous, len(st.History))
	}
	for i, v := range vs {
		has := fileExists(filepath.Join(e.installRoot, v))
		if want := i >= 2; has != want {
			t.Errorf("%s present=%v want %v", v, has, want)
		}
	}
	// redeploying the current version does not make it its own previous
	if err := e.deploy(e.build("1.4.0"), nil); err != nil {
		t.Fatal(err)
	}
	st, _ = LoadState(e.installRoot)
	if st.Previous.Version != "1.3.0" || len(st.History) != 3 {
		t.Fatalf("redeploy changed bookkeeping: %+v", st.Previous)
	}
}

func TestUnitDropinIdempotent(t *testing.T) {
	d, fr, out := newDeps(t)
	_ = out
	tg := &Target{UnitDropins: []Dropin{{Unit: "svc.service", Name: "20-fleet", Content: "[Service]\nExecStart=\n"}, {Unit: "b.service", Name: "x.conf", Content: "[Service]\n"}}}
	ch, err := d.writeDropins(tg, false)
	if err != nil || !ch {
		t.Fatalf("first: %v %v", ch, err)
	}
	p := filepath.Join(d.Home, ".config/systemd/user/svc.service.d/20-fleet.conf")
	if b, _ := os.ReadFile(p); string(b) != "[Service]\nExecStart=\n" {
		t.Fatalf("content %q", b)
	}
	if fr.count("systemctl --user daemon-reload") != 1 {
		t.Fatal("daemon-reload not run once")
	}
	fi1, _ := os.Stat(p)
	time.Sleep(20 * time.Millisecond)
	ch, err = d.writeDropins(tg, false)
	if err != nil || ch {
		t.Fatalf("second: %v %v", ch, err)
	}
	fi2, _ := os.Stat(p)
	if !fi1.ModTime().Equal(fi2.ModTime()) || fr.count("systemctl --user daemon-reload") != 1 {
		t.Fatal("unchanged drop-in was rewritten or reloaded")
	}
	tg.UnitDropins[0].Content = "[Service]\nExecStart=/x\n"
	if ch, _ = d.writeDropins(tg, false); !ch || fr.count("systemctl --user daemon-reload") != 2 {
		t.Fatal("changed content not rewritten")
	}
	// dry run writes nothing
	tg.UnitDropins[0].Content = "other"
	d.writeDropins(tg, true)
	if b, _ := os.ReadFile(p); string(b) == "other" {
		t.Fatal("dry run wrote")
	}
}

func TestRestartModes(t *testing.T) {
	ctx := context.Background()
	d, fr, out := newDeps(t)
	vars := map[string]string{"version": "9.9", "app": "x", "install_dir": "/i"}

	_, err := d.restartAll(ctx, []Restart{{Via: "systemd-user", Unit: "a.service"}, {Via: "command", Argv: []string{"do", "{version}", "{install_dir}"}}, {Via: "none"}}, vars, false, false)
	if err != nil || fr.calls[0] != "systemctl --user restart a.service" || fr.calls[1] != "do 9.9 /i" || len(fr.calls) != 2 {
		t.Fatalf("calls %v err %v", fr.calls, err)
	}
	fr.calls = nil

	// cerberus default: print, never run
	pending, err := d.restartAll(ctx, []Restart{{Via: "cerberus", Resource: "hub-nanite"}}, vars, false, false)
	if err != nil || !pending || len(fr.calls) != 0 {
		t.Fatalf("print mode: %v %v %v", pending, err, fr.calls)
	}
	mustContain(t, out.String(), "cerberus resource reload hub-nanite --ack", "REQUEST FOR A HUMAN")
	// operator confirmation proceeds without running anything
	pending, err = d.restartAll(ctx, []Restart{{Via: "cerberus", Resource: "hub-nanite", Verb: "restart"}}, vars, true, false)
	if err != nil || pending || len(fr.calls) != 0 {
		t.Fatalf("restart-done: %v %v %v", pending, err, fr.calls)
	}
	// run: true runs it locally
	pending, err = d.restartAll(ctx, []Restart{{Via: "cerberus", Resource: "nanite-api-service", Run: true}}, vars, false, false)
	if err != nil || pending || len(fr.calls) != 1 || fr.calls[0] != "cerberus resource reload nanite-api-service --ack" {
		t.Fatalf("run: %v %v %v", pending, err, fr.calls)
	}
	// ... but not when the user is not local
	fr.calls = nil
	d.LocalUser = false
	pending, _ = d.restartAll(ctx, []Restart{{Via: "cerberus", Resource: "r", Run: true}}, vars, false, false)
	if !pending || len(fr.calls) != 0 {
		t.Fatal("ran cerberus for a non-local user")
	}
}

func TestDeployCerberusPrintThenRestartDone(t *testing.T) {
	e := newEnv(t, `      - {via: cerberus, resource: hub-nanite}`, "")
	a := e.build("1.0.0")
	err := e.deploy(a, nil)
	if exitCode(err) != ExitAwaiting {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	mustContain(t, e.out.String(), "cerberus resource reload hub-nanite --ack")
	if e.fr.count("cerberus") != 0 {
		t.Fatal("cerberus was run")
	}
	if e.linkContent() != "1.0.0" {
		t.Fatal("install did not happen before waiting")
	}
	e.svc.restart(e.link) // the operator's restart
	if err := e.deploy(a, func(o *DeployOpts) { o.RestartDone = true }); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	st, _ := LoadState(e.installRoot)
	if st.Current.Version != "1.0.0" || st.Previous != nil {
		t.Fatalf("%+v", st)
	}
}

func TestHealth(t *testing.T) {
	ctx := context.Background()
	d, _, _ := newDeps(t)
	var code atomic.Int32
	code.Store(401)
	var body atomic.Value
	body.Store(`{"version":"v-1.2.3-gabc","nested":{"v":"x"}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(code.Load()))
		fmt.Fprint(w, body.Load().(string))
	}))
	defer srv.Close()
	h := Health{URL: srv.URL, ExpectStatus: []int{200, 401}, Timeout: "200ms", Interval: "10ms"}
	if err := d.checkHealth(ctx, []Health{h}, nil, false); err != nil {
		t.Fatalf("401 in expect list: %v", err)
	}
	h.ExpectStatus = []int{200}
	start := time.Now()
	err := d.checkHealth(ctx, []Health{h}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "status 401") || time.Since(start) < 150*time.Millisecond {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
	// recovers while polling
	go func() { time.Sleep(60 * time.Millisecond); code.Store(200) }()
	code.Store(503)
	h.Timeout = "2s"
	if err := d.checkHealth(ctx, []Health{h}, nil, false); err != nil {
		t.Fatalf("poll to success: %v", err)
	}
	// version_json
	h.VersionJSON = "version"
	h.Timeout = "50ms"
	if err := d.checkHealth(ctx, []Health{h}, []string{"1.2.3", "zzz"}, false); err != nil {
		t.Fatalf("version contains: %v", err)
	}
	if err := d.checkHealth(ctx, []Health{h}, []string{"9.9.9", "ffffffffffff"}, false); err == nil || !strings.Contains(err.Error(), "version_json") {
		t.Fatalf("version mismatch: %v", err)
	}
	if err := d.checkHealth(ctx, []Health{h}, []string{"gabc"}, false); err != nil {
		t.Fatalf("sha match: %v", err)
	}
	h.VersionJSON = "nested.v"
	if err := d.checkHealth(ctx, []Health{h}, []string{"x"}, false); err != nil {
		t.Fatalf("dotted path: %v", err)
	}
	// unreachable
	srv.Close()
	if err := d.checkHealth(ctx, []Health{{URL: srv.URL, Timeout: "30ms", Interval: "10ms"}}, nil, false); err == nil {
		t.Fatal("closed server passed")
	}
}

func TestAutoRollbackOnHealthFailure(t *testing.T) {
	needSqlite(t)
	db := filepath.Join(t.TempDir(), "main.db")
	mustSqlite(t, db, "create table t(x)")
	pf := fmt.Sprintf(`      - {kind: sqlite_backup, db: %s, dest: "{backup_dir}/main.pre-{version}.db"}`, db)
	e := newEnv(t, "", pf)
	if err := e.deploy(e.build("1.0.0"), nil); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	before := atomic.LoadInt32(&e.svc.restarts)
	e.svc.failures = 1
	err := e.deploy(e.build("2.0.0"), nil)
	if exitCode(err) != ExitRolledBack || !strings.Contains(err.Error(), "rolled back to 1.0.0") {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if e.linkContent() != "1.0.0" {
		t.Fatalf("link not restored: %q", e.linkContent())
	}
	if n := atomic.LoadInt32(&e.svc.restarts) - before; n != 2 {
		t.Fatalf("restart called %d times, want 2", n)
	}
	st, _ := LoadState(e.installRoot)
	if st.Current.Version != "1.0.0" {
		t.Fatalf("state current %s", st.Current.Version)
	}
	o := e.out.String()
	mustContain(t, o, "main.pre-2.0.0.db", "NEVER automatic", "cp -p", "rolling back to 1.0.0")
	if e.d.OS == "" || !strings.Contains(o, "rm -f") {
		t.Fatal("restore commands missing")
	}
	// the new version dir is kept for inspection; the DB was not touched
	if !fileExists(filepath.Join(e.installRoot, "2.0.0", "nanite")) {
		t.Fatal("failed version removed")
	}
}

func TestRollbackFailureReported(t *testing.T) {
	e := newEnv(t, "", "")
	e.deploy(e.build("1.0.0"), nil)
	e.svc.failures = 2 // the rollback restart is unhealthy too
	err := e.deploy(e.build("2.0.0"), nil)
	if exitCode(err) != ExitRollbackErr || !strings.Contains(err.Error(), "ROLLBACK ALSO FAILED") {
		t.Fatalf("%v", err)
	}
}

func TestManualRollback(t *testing.T) {
	e := newEnv(t, "", "")
	e.deploy(e.build("1.0.0"), nil)
	e.deploy(e.build("2.0.0"), nil)
	if e.linkContent() != "2.0.0" {
		t.Fatal("setup")
	}
	if err := e.d.Rollback(context.Background(), "nanite", "", e.state, false); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if e.linkContent() != "1.0.0" || e.svc.version != "1.0.0" {
		t.Fatalf("link %q service %q", e.linkContent(), e.svc.version)
	}
	st, _ := LoadState(e.installRoot)
	if st.Current.Version != "1.0.0" || st.Previous.Version != "2.0.0" {
		t.Fatalf("state %s/%s", st.Current.Version, st.Previous.Version)
	}
	if err := e.d.Rollback(context.Background(), "nanite", "", e.state, false); err != nil || e.linkContent() != "2.0.0" {
		t.Fatalf("roll forward: %v", err)
	}
	// nothing to roll back to
	e2 := newEnv(t, "", "")
	e2.deploy(e2.build("1.0.0"), nil)
	if err := e2.d.Rollback(context.Background(), "nanite", "", e2.state, false); err == nil || !strings.Contains(err.Error(), "no previous") {
		t.Fatalf("%v", err)
	}
	if err := e2.d.Rollback(context.Background(), "ghost", "", e2.state, false); err == nil {
		t.Fatal("rollback of unknown app")
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	needSqlite(t)
	db := filepath.Join(t.TempDir(), "main.db")
	mustSqlite(t, db, "create table t(x)", "create table goose_db_version(version_id integer)", "insert into goose_db_version values(3)")
	pf := fmt.Sprintf("      - {kind: sqlite_backup, db: %s, dest: \"{backup_dir}/b.db\"}\n      - {kind: migration_check, db: %s}", db, db)
	e := newEnv(t, "", pf)
	a := e.build("1.0.0")
	if err := e.deploy(a, nil); err != nil {
		t.Fatal(err)
	}
	b := buildArtifact(t, buildOpts{Version: "2.0.0", Binary: "2.0.0\n", Spec: e.spec, Migrations: 9})
	tree := func() string {
		return treeSnapshot(t, filepath.Dir(e.installRoot)) + treeSnapshot(t, e.state) + treeSnapshot(t, e.d.Home) + treeSnapshot(t, filepath.Dir(db))
	}
	before := tree()
	calls := len(e.fr.calls)
	err := e.deploy(b, func(o *DeployOpts) { o.DryRun = true })
	if exitCode(err) != ExitRefused { // the migration would stop it: reported, not executed
		t.Fatalf("dry-run: %v", err)
	}
	if err := e.deploy(b, func(o *DeployOpts) { o.DryRun = true; o.AcceptMigration = true }); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if after := tree(); after != before {
		t.Fatalf("dry-run changed the tree:\n--- before\n%s--- after\n%s", before, after)
	}
	for _, c := range e.fr.calls[calls:] {
		if strings.Contains(c, ".backup") || strings.HasPrefix(c, "fake-restart") || strings.HasPrefix(c, "systemctl") {
			t.Fatalf("dry-run ran %q", c)
		}
	}
	mustContain(t, e.out.String(), "would run sqlite3 .backup", "would flip symlink", "WOULD STOP", "health[0]: would poll", "dry-run complete")
	if e.linkContent() != "1.0.0" {
		t.Fatal("link moved")
	}
}

func TestScenarioDeployFailRollbackRedeployManualRollback(t *testing.T) {
	e := newEnv(t, "", "")
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%v\n%s", err, e.out.String())
		}
	}
	n := e.build("1.0.0")
	n1 := e.build("1.1.0")
	must(e.deploy(n, nil))
	e.svc.failures = 1
	if err := e.deploy(n1, nil); exitCode(err) != ExitRolledBack {
		t.Fatalf("expected automatic rollback: %v", err)
	}
	if e.linkContent() != "1.0.0" || e.svc.version != "1.0.0" || e.svc.status != 200 {
		t.Fatalf("not back on N: link %q svc %q/%d", e.linkContent(), e.svc.version, e.svc.status)
	}
	must(e.deploy(n1, nil))
	if e.linkContent() != "1.1.0" || e.svc.version != "1.1.0" {
		t.Fatal("redeploy did not land")
	}
	must(e.d.Rollback(ctx, "nanite", "", e.state, false))
	if e.linkContent() != "1.0.0" || e.svc.version != "1.0.0" {
		t.Fatal("manual rollback did not land")
	}
	must(e.d.Status(ctx, "nanite", "", e.state))
	mustContain(t, e.out.String(), "installed: 1.0.0", "previous:  1.1.0")
}

func TestDeployViaGHFake(t *testing.T) {
	e := newEnv(t, "", "")
	a := e.build("1.0.0")
	sha := strings.Repeat("ab", 20)
	e.fr.handler = func(name string, args []string) (string, error, bool) {
		line := name + " " + strings.Join(args, " ")
		switch {
		case name == "fake-restart":
			return "", e.svc.restart(e.link), true
		case strings.HasPrefix(line, "gh run list"):
			if !strings.Contains(line, "--commit "+sha) || !strings.Contains(line, "-R hollis-labs/nanite") {
				return "", fmt.Errorf("unexpected %s", line), true
			}
			return `[{"databaseId":77,"headSha":"` + sha + `","conclusion":"success","status":"completed"}]`, nil, true
		case strings.HasPrefix(line, "gh run download 77"):
			dir := args[len(args)-1]
			if !strings.Contains(line, "-n nanite-abababababab") {
				return "", fmt.Errorf("artifact name: %s", line), true
			}
			for _, f := range []string{a.Tarball, a.Sums, a.BuildInfo} {
				b, _ := os.ReadFile(f)
				os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644)
			}
			return "", nil, true
		}
		return "", nil, false
	}
	err := e.deploy(a, func(o *DeployOpts) {
		o.Source = Source{Commit: sha, Repo: "hollis-labs/nanite"}
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	reg, _ := LoadRegistry(e.state, "nanite")
	if reg.Repo != "hollis-labs/nanite" {
		t.Fatal("repo not remembered")
	}
	// no green run: refused
	e.fr.handler = func(name string, args []string) (string, error, bool) {
		if strings.HasPrefix(name+" "+strings.Join(args, " "), "gh run list") {
			return "[]", nil, true
		}
		return "", nil, false
	}
	err = e.deploy(a, func(o *DeployOpts) { o.Source = Source{Commit: sha} })
	if exitCode(err) != ExitRefused || !strings.Contains(err.Error(), "no successful") {
		t.Fatalf("%v", err)
	}
}

func TestCLI(t *testing.T) {
	d, _, out := newDeps(t)
	ctx := context.Background()
	if c := run(ctx, d, []string{"version"}); c != 0 || !strings.Contains(out.String(), "fleet-install") {
		t.Fatal("version")
	}
	for _, args := range [][]string{{}, {"bogus"}, {"deploy"}, {"deploy", "nanite"}, {"deploy", "nanite", "--commit", "x", "--run", "1"}, {"rollback"}, {"deploy", "Bad App", "--from-file", "x"}} {
		if c := run(ctx, d, args); c != ExitUsage {
			t.Errorf("%v -> %d", args, c)
		}
	}
	if c := run(ctx, d, []string{"deploy", "nanite", "--from-file", "/nonexistent.tar.gz", "--root", t.TempDir()}); c != ExitFail {
		t.Errorf("missing file -> %d", c)
	}
	if c := run(ctx, d, []string{"list", "--root", t.TempDir()}); c != 0 {
		t.Error("list")
	}
}
