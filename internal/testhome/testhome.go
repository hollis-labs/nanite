// Package testhome keeps a test binary out of the real user's home and XDG
// directories (CW-20260930-0208).
//
// go-apppaths materializes nanite's layout under $XDG_*_HOME (default
// $HOME/.local/share, .local/state, .cache, .config) and, from v0.2.0, chmods
// what it finds there to 0700. A test that reached a materializing
// config.ResolveLayout without its own HOME/XDG did that to the developer's
// real ~/.local/share/nanite and ~/.cache/nanite during a gate run, and
// internal/plugin's lazy plugin state store would open the real main
// database the same way. A package whose tests can reach either calls Main
// (or Run) from its TestMain.
package testhome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	paths "github.com/hollis-labs/libs/util/apppaths"
	"github.com/hollis-labs/nanite/internal/brand"
)

var (
	realHome string
	tempRoot string
)

// Main is a TestMain that needs nothing else:
//
//	func TestMain(m *testing.M) { testhome.Main(m) }
func Main(m *testing.M) {
	os.Exit(Run(m))
}

// Run isolates the binary (Isolate), runs the tests and removes the temp tree,
// returning m.Run's exit code (1 when isolation fails). It is for a TestMain
// with work of its own before or after.
func Run(m *testing.M) int {
	cleanup, err := Isolate()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "testhome: %v\n", err)
		return 1
	}
	defer cleanup()
	return m.Run()
}

// Isolate points HOME and XDG_{DATA,STATE,CACHE,CONFIG}_HOME at a fresh temp
// tree and drops the NANITE_DB_PATH, NANITE_DB and NANITE_WORKSPACE overrides
// a developer's shell may carry, then refuses if nanite's layout would still
// resolve under the real home. The Go toolchain's GOCACHE, GOPATH, GOMODCACHE
// and GOENV are pinned to their pre-isolation locations first, so a test
// that runs `go` keeps the real build cache and private-module settings.
func Isolate() (cleanup func(), err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve real home: %w", err)
	}
	realHome = home
	if err = pinGoToolchain(home); err != nil {
		return nil, err
	}

	root, err := os.MkdirTemp("", "nanite-test-home-")
	if err != nil {
		return nil, fmt.Errorf("create temp home: %w", err)
	}
	tempRoot = root
	cleanup = func() { _ = os.RemoveAll(root) }
	fail := func(err error) (func(), error) {
		cleanup()
		return nil, err
	}
	if under(home, root) {
		return fail(fmt.Errorf("temp dir %s is under the real home %s; set TMPDIR outside it", root, home))
	}
	testHome := filepath.Join(root, "home")
	if err := os.MkdirAll(testHome, 0o700); err != nil {
		return fail(fmt.Errorf("create temp home: %w", err))
	}
	for env, dir := range map[string]string{
		"HOME":            testHome,
		"XDG_DATA_HOME":   filepath.Join(root, "xdg", "data"),
		"XDG_STATE_HOME":  filepath.Join(root, "xdg", "state"),
		"XDG_CACHE_HOME":  filepath.Join(root, "xdg", "cache"),
		"XDG_CONFIG_HOME": filepath.Join(root, "xdg", "config"),
	} {
		if err := os.Setenv(env, dir); err != nil {
			return fail(fmt.Errorf("set %s: %w", env, err))
		}
	}
	for _, env := range []string{brand.Env("DB_PATH"), brand.Env("DB"), brand.Env("WORKSPACE")} {
		if err := os.Unsetenv(env); err != nil {
			return fail(fmt.Errorf("unset %s: %w", env, err))
		}
	}

	if err := checkLayoutOutside(home); err != nil {
		return fail(err)
	}
	return cleanup, nil
}

// Root returns the temp tree Isolate created (HOME is Root()/home, the XDG
// homes are under Root()/xdg), or "" before Isolate has run. A package that
// keeps more test state there, and asserts paths land under it, uses this.
func Root() string {
	return tempRoot
}

// RealHome returns the home directory the binary had before Isolate, or ""
// before Isolate has run.
func RealHome() string {
	return realHome
}

// AssertOutsideRealHome fails t if any dir is at or under the real home.
func AssertOutsideRealHome(t testing.TB, dirs ...string) {
	t.Helper()
	if realHome == "" {
		t.Fatal("testhome: Isolate has not run; call testhome.Main or Run from TestMain")
	}
	for _, dir := range dirs {
		if under(realHome, dir) {
			t.Errorf("%s is under the real home %s", dir, realHome)
		}
	}
}

// checkLayoutOutside resolves nanite's layout without materializing it and
// fails if any of its roots, or the main database, is under home.
func checkLayoutOutside(home string) error {
	layout, err := paths.Resolve(brand.ID, paths.WithoutMaterialize())
	if err != nil {
		return fmt.Errorf("resolve layout: %w", err)
	}
	var errs []error
	for _, dir := range []string{layout.DataDir(), layout.StateDir(), layout.CacheDir(), layout.ConfigDir(), layout.MainDB()} {
		if under(home, dir) {
			errs = append(errs, fmt.Errorf("layout path %s is under the real home %s", dir, home))
		}
	}
	return errors.Join(errs...)
}

func pinGoToolchain(home string) error {
	cacheDir, cacheErr := os.UserCacheDir()
	configDir, configErr := os.UserConfigDir()
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(home, "go")
	}
	pins := map[string]string{
		"GOPATH":     gopath,
		"GOMODCACHE": filepath.Join(gopath, "pkg", "mod"),
	}
	if cacheErr == nil {
		pins["GOCACHE"] = filepath.Join(cacheDir, "go-build")
	}
	if configErr == nil {
		pins["GOENV"] = filepath.Join(configDir, "go", "env")
	}
	for env, value := range pins {
		if os.Getenv(env) != "" {
			continue
		}
		if err := os.Setenv(env, value); err != nil {
			return fmt.Errorf("pin %s: %w", env, err)
		}
	}
	return nil
}

func under(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
