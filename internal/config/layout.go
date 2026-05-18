// Layout resolution for nanite's on-disk paths via go-apppaths.
//
// CW-20260517-0061 (child of CW-20260517-0058 — XDG-everywhere storage
// paths). This retires nanite's CWD-relative `./nanite.db` defaults: the
// main database, plugin-data, and plugin-cache now resolve through the
// go-apppaths XDG layout instead of `filepath.Dir(cwd)`.
//
// Path model after migration (appName = brand.ID = "nanite", XDG mode, no
// project mode):
//
//	main DB        ~/.local/share/nanite/workspaces/default/main.db
//	plugin-data    ~/.local/share/nanite/plugin-data/<id>   (DataDir)
//	plugin-cache   ~/.cache/nanite/plugin-cache/<id>        (CacheDir)
//	coordination/  ~/.local/state/nanite/coordination       (StateDir)
//	worktrees/     ~/.local/state/nanite/worktrees          (StateDir)
//	config.yaml    ~/.config/nanite/config.yaml             (already XDG)
//
// brand.ID is the single source of truth for the app name — it IS the
// go-apppaths appName, and the NANITE_ env prefix go-apppaths derives from it
// (NANITE_DB_PATH, NANITE_WORKSPACE) agrees with brand.EnvPrefix. Do not
// duplicate dir/env logic between brand and this resolver.
package config

import (
	"github.com/hollis-labs/go-apppaths/paths"
	"github.com/hollis-labs/nanite/internal/brand"
)

// ResolveLayout resolves nanite's on-disk layout via go-apppaths in the
// default (XDG) mode. Project mode is deliberately not used — the CWD-local
// layout is the data-loss failure mode CW-20260517-0061 removes.
//
// No WithLegacyNames: nanite has never had a prior XDG-root app name (brand.ID
// has always been "nanite"), and the hand-made ~/.nanite dotdir is not
// reachable by WithLegacyNames anyway (it migrates XDG-root → XDG-root keyed
// by app name). The ~/.nanite dotdir evacuation is a separate follow-up.
//
// Callers that only introspect (the `nanite path` subcommand) pass
// paths.WithoutMaterialize() so resolution never creates directories.
func ResolveLayout(extra ...paths.Option) (paths.Layout, error) {
	return paths.Resolve(brand.ID, extra...)
}

// tesseractAppName is the go-apppaths appName of the Tesseract memory store
// nanite embeds (github.com/hollis-labs/tesseract). It is intentionally NOT
// brand.ID — this resolves a *different* application's layout.
const tesseractAppName = "tesseract"

// ResolveTesseractLayout resolves the on-disk layout of the embedded
// Tesseract memory store via go-apppaths, so nanite can point conduit.Open at
// Tesseract's migrated context.db / records/ directly (CW-20260517-0061)
// rather than relying on the Phase 2 ~/.tesseract → XDG compat symlink.
//
// Tesseract migrated under CW-20260517-0066: its main DB is
// ~/.local/share/tesseract/workspaces/default/main.db and its records/ tree
// is ~/.local/state/tesseract/records. WithoutMaterialize keeps this a pure
// path computation — conduit.Open MkdirAll's the directories it needs.
//
// This honors the TESSERACT_DB_PATH / TESSERACT_WORKSPACE env vars natively,
// matching what the standalone contextd daemon resolves.
func ResolveTesseractLayout(extra ...paths.Option) (paths.Layout, error) {
	opts := append([]paths.Option{paths.WithoutMaterialize()}, extra...)
	return paths.Resolve(tesseractAppName, opts...)
}
