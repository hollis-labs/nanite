package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/apiauth"
)

// enforcedAuth builds a real, enforcing guard over a temporary credential
// store and returns it with an operator secret.
func enforcedAuth(t *testing.T) (Auth, string) {
	t.Helper()
	ctx := context.Background()
	st, err := apiauth.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, secret, err := st.Create(ctx, apiauth.CreateParams{Kind: apiauth.KindOperator, Label: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return Auth{Guard: apiauth.NewGuard(st)}, secret
}

// openGuard is the explicit development mode, for tests of everything that is
// not authentication.
func openGuard() *apiauth.Guard {
	g := apiauth.NewGuard(nil)
	g.Insecure = true
	return g
}

// enforce replaces a test server's guard with an enforcing one and returns
// the operator secret.
func (s *Server) enforce(t *testing.T) string {
	t.Helper()
	auth, secret := enforcedAuth(t)
	s.guard = auth.Guard
	s.guard.Pattern = func(r *http.Request) string { _, p := s.mux.Handler(r); return p }
	return secret
}
