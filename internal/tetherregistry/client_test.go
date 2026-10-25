package tetherregistry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/tetherregistry"
	"github.com/hollis-labs/nanite/internal/tetherregistry/tetherregistrytest"
)

const key = "nanite/install-1/host-1"

func TestReadTokenRefusesBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := tetherregistry.ReadToken(""); !errors.Is(err, tetherregistry.ErrTokenMissing) {
		t.Errorf("empty path: %v", err)
	}
	if _, err := tetherregistry.ReadToken(filepath.Join(dir, "nope")); !errors.Is(err, tetherregistry.ErrTokenMissing) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := tetherregistry.ReadToken(write("empty", "  \n", 0o600)); !errors.Is(err, tetherregistry.ErrTokenMissing) {
		t.Errorf("empty file: %v", err)
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o666, 0o607} {
		if _, err := tetherregistry.ReadToken(write("loose", "tok", mode)); !errors.Is(err, tetherregistry.ErrTokenInsecure) {
			t.Errorf("mode %o: %v, want ErrTokenInsecure", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o400} {
		if tok, err := tetherregistry.ReadToken(write("ok", " tok-value \n", mode)); err != nil || tok != "tok-value" {
			t.Errorf("mode %o: %q %v", mode, tok, err)
		}
	}
}

func TestClientNotConfiguredAndTokenErrorsMakeNoRequest(t *testing.T) {
	f := tetherregistrytest.New(t)
	none := &tetherregistry.Client{Config: func() tetherregistry.Config { return tetherregistry.Config{} }}
	if err := none.Ready(); !errors.Is(err, tetherregistry.ErrNotConfigured) {
		t.Fatalf("no addr: %v", err)
	}
	if _, _, err := none.Register(context.Background(), key, "x"); !errors.Is(err, tetherregistry.ErrNotConfigured) {
		t.Fatalf("register no addr: %v", err)
	}
	noTok := f.Registrar("")
	if err := noTok.Ready(); !errors.Is(err, tetherregistry.ErrTokenMissing) {
		t.Fatalf("no token: %v", err)
	}
	loose := filepath.Join(t.TempDir(), "t")
	_ = os.WriteFile(loose, []byte("x"), 0o644)
	_ = os.Chmod(loose, 0o644)
	if _, _, err := f.Registrar(loose).Register(context.Background(), key, "x"); !errors.Is(err, tetherregistry.ErrTokenInsecure) {
		t.Fatalf("insecure token: %v", err)
	}
	if r, l, d := f.Counts(); r+l+d != 0 {
		t.Fatalf("configuration errors still reached Tether: %d %d %d", r, l, d)
	}
}

func TestRegisterIsIdempotentLookupAndDeregister(t *testing.T) {
	f := tetherregistrytest.New(t)
	c := f.Registrar(tetherregistrytest.TokenFile(t))
	ctx := context.Background()

	urn, created, err := c.Register(ctx, key, "Host One")
	if err != nil || !created || !strings.HasPrefix(urn, "msg://agent/agent-mux/agt_") {
		t.Fatalf("first: %q %v %v", urn, created, err)
	}
	again, created, err := c.Register(ctx, key, "Host One")
	if err != nil || created || again != urn {
		t.Fatalf("repeat: %q %v %v", again, created, err)
	}
	if f.LastAuth != "Bearer "+tetherregistrytest.Token {
		t.Fatal("registry token was not sent as the bearer")
	}
	if got, ok, err := c.Lookup(ctx, key); err != nil || !ok || got != urn {
		t.Fatalf("lookup: %q %v %v", got, ok, err)
	}
	if _, ok, err := c.Lookup(ctx, "nanite/install-1/other"); err != nil || ok {
		t.Fatalf("lookup of an unknown key: %v %v", ok, err)
	}
	if err = c.Deregister(ctx, urn); err != nil {
		t.Fatal(err)
	}
	// The URN rides as one escaped path segment.
	last := f.Log[len(f.Log)-1]
	if last != "DELETE /registry/agents/msg:%2F%2Fagent%2Fagent-mux%2F"+strings.TrimPrefix(urn, "msg://agent/agent-mux/") {
		t.Fatalf("deregister request = %q", last)
	}
	if es := f.Entries(); len(es) != 1 || es[0].Status != "deprecated" {
		t.Fatalf("entries = %+v", es)
	}
	if err = c.Deregister(ctx, "msg://agent/agent-mux/agt_gone"); err != nil {
		t.Fatalf("an entry Tether no longer has counts as done: %v", err)
	}
	if _, _, err = c.Register(ctx, "no-slash", "x"); err == nil {
		t.Fatal("half a key was accepted client-side")
	}
}

func TestFailureModesAreTyped(t *testing.T) {
	f := tetherregistrytest.New(t)
	c := f.Registrar(tetherregistrytest.TokenFile(t))
	ctx := context.Background()

	f.FailWith(400, "invalid_request")
	_, _, err := c.Register(ctx, key, "x")
	var ref *tetherregistry.RefusedError
	if !errors.As(err, &ref) || ref.Status != 400 || ref.Code != "invalid_request" || ref.Unauthorized() {
		t.Fatalf("400: %v", err)
	}
	for _, status := range []int{401, 403} {
		f.FailWith(status, "forbidden")
		_, _, err = c.Register(ctx, key, "x")
		if !errors.As(err, &ref) || ref.Status != status || !ref.Unauthorized() {
			t.Fatalf("%d: %v", status, err)
		}
	}
	f.FailWith(503, "internal_error")
	if _, _, err = c.Register(ctx, key, "x"); !errors.Is(err, tetherregistry.ErrUnreachable) {
		t.Fatalf("5xx: %v", err)
	}
	f.Drop()
	if _, _, err = c.Register(ctx, key, "x"); !errors.Is(err, tetherregistry.ErrUnreachable) {
		t.Fatalf("dropped connection: %v", err)
	}
	f.Slow(300 * time.Millisecond)
	short := f.Registrar(tetherregistrytest.TokenFile(t))
	short.HTTP.Timeout = 50 * time.Millisecond
	if _, _, err = short.Register(ctx, key, "x"); !errors.Is(err, tetherregistry.ErrUnreachable) {
		t.Fatalf("timeout: %v", err)
	}
	f.Heal()
	// A wrong token is refused by the fake as 401, with no echo of the token.
	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("wrong-fake-token"), 0o600)
	_, _, err = f.Registrar(bad).Register(ctx, key, "x")
	if !errors.As(err, &ref) || ref.Status != 401 || strings.Contains(err.Error(), "wrong-fake-token") {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestUnixSocketTransport(t *testing.T) {
	f, sock := tetherregistrytest.NewUnix(t)
	tok := tetherregistrytest.TokenFile(t)
	ctx := context.Background()
	for _, addr := range []string{"unix://" + sock, sock} {
		c := &tetherregistry.Client{Config: func() tetherregistry.Config { return tetherregistry.Config{Addr: addr, TokenFile: tok} }}
		if err := c.Ready(); err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
	}
	c := f.Registrar(tok)
	if _, ok, err := c.Lookup(ctx, key); err != nil || ok {
		t.Fatalf("lookup 404: %v %v", ok, err)
	}
	urn, created, err := c.Register(ctx, key, "Host One")
	if err != nil || !created {
		t.Fatalf("register: %q %v %v", urn, created, err)
	}
	if again, created, err := c.Register(ctx, key, "Host One"); err != nil || created || again != urn {
		t.Fatalf("repeat: %q %v %v", again, created, err)
	}
	if got, ok, err := c.Lookup(ctx, key); err != nil || !ok || got != urn {
		t.Fatalf("lookup 200: %q %v %v", got, ok, err)
	}
	if f.LastAuth != "Bearer "+tetherregistrytest.Token {
		t.Fatal("Authorization header missing on the socket")
	}
	if f.LastHost != "localhost" {
		t.Fatalf("Host = %q, want localhost", f.LastHost)
	}
	lookup := ""
	for _, l := range f.Log {
		if strings.HasPrefix(l, "GET ") {
			lookup = l
		}
	}
	if lookup != "GET /registry/agents" {
		t.Fatalf("lookup path = %q", lookup)
	}
	if err = c.Deregister(ctx, urn); err != nil {
		t.Fatal(err)
	}
	if last := f.Log[len(f.Log)-1]; !strings.HasPrefix(last, "DELETE /registry/agents/msg:%2F%2Fagent%2F") {
		t.Fatalf("deregister = %q", last)
	}
	if es := f.Entries(); es[0].Status != "deprecated" {
		t.Fatalf("entries = %+v", es)
	}
	// 401 maps as on TCP, and never echoes the token.
	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("wrong-fake-token"), 0o600)
	var ref *tetherregistry.RefusedError
	_, _, err = f.Registrar(bad).Register(ctx, key, "x")
	if !errors.As(err, &ref) || ref.Status != 401 || strings.Contains(err.Error(), "wrong-fake-token") {
		t.Fatalf("401: %v", err)
	}
}

func TestRegistryAddressResolutionAndSocketErrors(t *testing.T) {
	tok := tetherregistrytest.TokenFile(t)
	_, sock := tetherregistrytest.NewUnix(t)
	t.Setenv(tetherregistry.TokenFileEnv, tok)

	// Neither set: not configured.
	t.Setenv(tetherregistry.AddrEnv, "")
	t.Setenv(tetherregistry.RegistryAddrEnv, "")
	if err := (&tetherregistry.Client{}).Ready(); !errors.Is(err, tetherregistry.ErrNotConfigured) {
		t.Fatalf("none: %v", err)
	}
	// Fallback to the bridge address.
	t.Setenv(tetherregistry.AddrEnv, "unix://"+sock)
	if cfg := tetherregistry.ConfigFromEnv(); cfg.Addr != "unix://"+sock {
		t.Fatalf("fallback addr = %q", cfg.Addr)
	}
	if err := (&tetherregistry.Client{}).Ready(); err != nil {
		t.Fatalf("fallback ready: %v", err)
	}
	// The registry address wins and leaves the bridge variable alone.
	t.Setenv(tetherregistry.RegistryAddrEnv, "https://tether.example.test")
	if cfg := tetherregistry.ConfigFromEnv(); cfg.Addr != "https://tether.example.test" {
		t.Fatalf("registry addr = %q", cfg.Addr)
	}
	if os.Getenv(tetherregistry.AddrEnv) != "unix://"+sock {
		t.Fatal("the bridge address was changed")
	}

	c := func(addr string) error {
		return (&tetherregistry.Client{Config: func() tetherregistry.Config { return tetherregistry.Config{Addr: addr, TokenFile: tok} }}).Ready()
	}
	if err := c("unix://relative/tetherd.sock"); !errors.Is(err, tetherregistry.ErrAddrInvalid) {
		t.Errorf("relative: %v", err)
	}
	if err := c("ftp://x/y"); !errors.Is(err, tetherregistry.ErrAddrInvalid) {
		t.Errorf("scheme: %v", err)
	}
	if err := c("unix:///does/not/exist.sock"); !errors.Is(err, tetherregistry.ErrUnreachable) {
		t.Errorf("missing socket: %v", err)
	}
	if err := c(tok); !errors.Is(err, tetherregistry.ErrAddrInvalid) { // a regular file, not a socket
		t.Errorf("not a socket: %v", err)
	}
	if err := c("unix:///a/../b.sock"); !errors.Is(err, tetherregistry.ErrAddrInvalid) {
		t.Errorf("unclean path: %v", err)
	}
	if err := c("http://127.0.0.1:1"); err != nil {
		t.Errorf("http url: %v", err)
	}
}
