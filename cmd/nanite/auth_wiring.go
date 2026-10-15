package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	paths "github.com/hollis-labs/libs/util/apppaths"
	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/store"
)

// authPaths locates the credential database and the operator token file.
// They live in their own directory, apart from main.db, so restoring a
// main.db backup cannot resurrect a revoked credential (CW-20261011-0036).
type authPaths struct {
	dir   string
	db    string
	token string
}

func authPathsAt(dir string) authPaths {
	return authPaths{dir: dir, db: filepath.Join(dir, "auth.db"), token: filepath.Join(dir, "operator.token")}
}

// resolveAuthPaths is the single place the server and every CLI command agree
// on where the credentials live. The auth directory sits beside the database
// whenever the operator named one — a --db flag (dbFlag, else the subcommand's
// own, cliDB) or NANITE_DB / NANITE_DB_PATH — so tests and throwaway
// instances stay self-contained and `serve -db X` is found again by
// `NANITE_DB_PATH=X nanite auth ...`. With no explicit database (default or
// NANITE_WORKSPACE) it is in the data directory.
//
// An instance that set only NANITE_DB_PATH before this agreed with the CLI
// kept its credentials in the data directory; that directory is still used
// when it holds a credential database and the one beside the db does not, so
// an upgrade never silently mints a new operator.
func resolveAuthPaths(dbFlag string) (authPaths, error) {
	if dbFlag == "" {
		dbFlag = cliDB
	}
	if dbFlag != "" {
		abs, err := filepath.Abs(dbFlag)
		if err != nil {
			return authPaths{}, err
		}
		return authPathsAt(filepath.Join(filepath.Dir(abs), "auth")), nil
	}
	var opts []paths.Option
	if legacy := os.Getenv(brand.Env("DB")); legacy != "" {
		opts = append(opts, paths.WithDBOverride(legacy))
	}
	layout, err := config.ResolveLayout(append(opts, paths.WithoutMaterialize())...)
	if err != nil {
		return authPaths{}, fmt.Errorf("resolve app layout: %w", err)
	}
	dataAuth := authPathsAt(filepath.Join(layout.DataDir(), "auth"))
	if os.Getenv(brand.Env("DB")) == "" && os.Getenv(brand.Env("DB_PATH")) == "" {
		return dataAuth, nil
	}
	besideDB := authPathsAt(filepath.Join(filepath.Dir(layout.MainDB()), "auth"))
	if fileExists(dataAuth.db) && !fileExists(besideDB.db) {
		return dataAuth, nil
	}
	return besideDB, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// serveAuth is what `nanite serve` hands the server.
type serveAuth struct {
	guard           *apiauth.Guard
	store           *apiauth.Store
	paths           authPaths
	agentCredential func(sessionID string) (string, error)
}

// initServeAuth opens the credential store, guarantees an operator
// credential (writing the token file on first start) and builds the guard.
func initServeAuth(ctx context.Context, dbFlag string, main *store.Store, insecure bool) (*serveAuth, error) {
	p, err := resolveAuthPaths(dbFlag)
	if err != nil {
		return nil, err
	}
	if mkErr := os.MkdirAll(p.dir, 0o700); mkErr != nil {
		return nil, fmt.Errorf("create auth directory: %w", mkErr)
	}
	st, err := apiauth.Open(ctx, p.db)
	if err != nil {
		return nil, err
	}
	if _, err := apiauth.EnsureOperator(ctx, st, p.token, os.Getenv(apiauth.EnvTokenVar)); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("operator credential: %w", err)
	}
	g := apiauth.NewGuard(st)
	g.Insecure = insecure
	g.SessionLive = func(ctx context.Context, sessionID string) bool {
		sess, err := main.GetSession(ctx, sessionID)
		return err == nil && sess != nil && sess.Status != "archived"
	}
	mint := func(sessionID string) (string, error) {
		label := "agent-" + sessionID
		if len(label) > 24 {
			label = label[:24]
		}
		_, secret, err := st.Create(context.Background(), apiauth.CreateParams{Kind: apiauth.KindAgent, Label: label, BoundSessionID: sessionID, TTL: time.Hour, CreatedBy: "host"})
		return secret, err
	}
	if insecure {
		slog.Warn("--insecure-no-auth: authentication is DISABLED for this run (development only)")
	}
	return &serveAuth{guard: g, store: st, paths: p, agentCredential: mint}, nil
}

// applyClientAuth presents the operator credential on a local client request:
// the NANITE_AUTH_TOKEN override, else the operator token file. With neither
// it adds nothing and the server answers 401 with a clear message.
func applyClientAuth(req *http.Request) {
	tokenPath := ""
	if p, err := resolveAuthPaths(""); err == nil {
		tokenPath = p.token
	}
	if token, err := apiauth.ClientToken(tokenPath); err == nil {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
