package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdAgent dispatches `nanite agent <install|sync> ...` — the CLI trigger for
// CW-20260910-0009's explicit, one-way agent import pipeline
// (internal/agentimport.Importer).
//
// The shape mirrors cmd/nanite/skill_cmd.go deliberately, `sync` included.
// Skills settled the identical question first ("authored packages installed
// explicitly, not a boot-time file-reingest target") and the pair is the
// whole answer: `install` registers something new, and `sync` is the honest
// answer to "I edited the source file." Shipping `install` without `sync`
// is what pushes an operator back toward wanting a watcher, which is exactly
// the boot-time re-read this codebase removed on purpose.
//
// Like skill_cmd.go, this command opens the same SQLite database directly
// (store.New + resolveDBPath) rather than requiring a running `nanite serve`:
// an import writes DB state a running service and this CLI invocation
// genuinely share, and the store's own WAL + busy_timeout(5s) pragmas
// (internal/store/store.go) are what make that safe across processes. See
// skill_cmd.go's header for the full rationale — this follows the same
// multi-process-CLI convention rather than inventing a second one.
//
// What this command is NOT: it registers no watch, schedules nothing, and
// adds no tier to internal/agent/discovery.go. Every write here is the direct
// result of one operator invocation naming one path.
func cmdAgent(args []string) {
	if len(args) < 1 {
		agentUsage()
		os.Exit(1)
	}

	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("agent "+sub, flag.ExitOnError)
	adapterFlag := fs.String("adapter", "", "force a specific format adapter (e.g. claude) instead of trying each in priority order")
	if err := fs.Parse(rest); err != nil {
		os.Exit(1)
	}
	positional := fs.Args()

	switch sub {
	case "install":
		if len(positional) < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s agent install [--adapter <name>] <path>\n", brand.BinaryName)
			os.Exit(1)
		}
		agentInstallCmd(positional[0], *adapterFlag)
	case "sync":
		if len(positional) < 2 {
			fmt.Fprintf(os.Stderr, "usage: %s agent sync [--adapter <name>] <slug> <path>\n", brand.BinaryName)
			os.Exit(1)
		}
		agentSyncCmd(positional[0], positional[1], *adapterFlag)
	default:
		fmt.Fprintf(os.Stderr, "unknown agent command: %s\n", args[0])
		agentUsage()
		os.Exit(1)
	}
}

func agentUsage() {
	fmt.Fprintf(os.Stderr, "usage: %s agent <install|sync> ...\n", brand.BinaryName)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  install <path>        import an agent definition from a local path")
	fmt.Fprintln(os.Stderr, "  sync <slug> <path>    re-run import for an already-imported agent")
	fmt.Fprintln(os.Stderr, "flags:")
	fmt.Fprintln(os.Stderr, "  --adapter <name>      force a format adapter instead of trying each in priority order")
}

// openAgentImporter opens the shared DB and returns a fresh Importer plus the
// store handle for the caller to close. A fresh Importer per invocation
// matches skill_cmd.go's openSkillInstaller.
func openAgentImporter(adapterName string) (*agentimport.Importer, *store.Store) {
	dbPath := resolveDBPath()
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		os.Exit(1)
	}

	return &agentimport.Importer{
		Store:        s,
		Parse:        agentImportParser(adapterName),
		SeedChildren: service.SeedImportedAgentChildren(s),
		Emit:         printAgentImportEvents(),
	}, s
}

// agentImportParser builds the format chain: Nanite's own format first
// because a definition authored for Nanite is the ordinary case, then the
// registered format adapters in priority order (CW-20260910-0012's re-armed
// seam). `--adapter <name>` skips the chain and names one, for when the guess
// would be wrong.
//
// This is the import direction of the same AdapterRegistry
// internal/service/container.go builds for sandbox population and
// project-root sync. It is constructed here, at an operator-invoked entry
// point, and never at boot.
func agentImportParser(adapterName string) agentimport.Parser {
	registry := service.NewImportAdapterRegistry()
	if adapterName != "" {
		return agentimport.RegistryParser{Registry: registry, Adapter: adapterName}
	}
	return agentimport.ChainParser{Parsers: []agentimport.Parser{
		agentimport.NativeParser{},
		agentimport.RegistryParser{Registry: registry},
	}}
}

// printAgentImportEvents mirrors skill_cmd.go's printSkillInstallEvents:
// throttled, human-readable step progress on stdout.
func printAgentImportEvents() agentimport.EventFunc {
	last := agentimport.State("")
	return func(e agentimport.Event) {
		if e.State != last {
			fmt.Printf("  [%s] %s\n", e.State, e.Message)
			last = e.State
		}
		if e.Err != nil {
			fmt.Printf("  failure: %v\n", e.Err)
		}
	}
}

// agentInstallCmd runs `nanite agent install <path>`.
//
// A path that expands to more than one definition is reported per definition
// — this is the "directory argument is sugar, not a tier" contract: N single
// imports and a report of what landed and what did not, with nothing
// remembered about the directory afterwards.
func agentInstallCmd(path, adapterName string) {
	importer, s := openAgentImporter(adapterName)
	defer closeStoreBestEffort(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, s)

	result, err := importer.Import(context.Background(), agentimport.Source{Path: path})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s agent install: failed: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}

	printAgentImportResult(result)
	// A skipped definition is a refusal to overwrite a profile import does
	// not own, not a partial success to shrug at. Exit non-zero so a script
	// notices, after printing every outcome so the operator sees the whole
	// picture rather than the first problem.
	if result.Skipped() {
		os.Exit(1)
	}
}

// agentSyncCmd runs `nanite agent sync <slug> <path>`.
//
// It requires slug to already exist (a clear error, not a silent create) and
// requires the definition at path to declare that same slug — mirroring
// skillSyncCmd's guard so both commands reject the same "wrong source for
// this slug" mistake before anything is written. It also requires the
// existing row to be one import owns: syncing over an operator-managed or
// internal profile is refused here for the same reason install refuses it.
func agentSyncCmd(slug, path, adapterName string) {
	importer, s := openAgentImporter(adapterName)
	defer closeStoreBestEffort(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, s)

	ctx := context.Background()
	existing, err := s.GetAgentBySlug(ctx, slug)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fmt.Fprintf(os.Stderr, "%s agent sync: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	if existing == nil {
		fmt.Fprintf(os.Stderr, "%s agent sync: agent %q not found — run `%s agent install %s` first\n",
			brand.BinaryName, slug, brand.BinaryName, path)
		os.Exit(1)
	}
	if class := agentpkg.NewClassification().Classify(existing.Source); class != agentpkg.ManageClassExternal {
		fmt.Fprintf(os.Stderr, "%s agent sync: agent %q is %s (source=%q), not an imported one — import never overwrites it\n",
			brand.BinaryName, slug, agentimport.DescribeClass(class), existing.Source)
		os.Exit(1)
	}

	// Parse before importing so a mismatched sync target is rejected without
	// writing anything — the same pre-flight ordering handleSyncSkill uses.
	// The same parser the import will use, so the two cannot disagree about
	// what the source declares.
	defs, err := agentImportParser(adapterName).Parse(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s agent sync: parse: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	if !definesSlug(defs, slug) {
		fmt.Fprintf(os.Stderr, "%s agent sync: source at %q does not declare slug %q\n",
			brand.BinaryName, path, slug)
		os.Exit(1)
	}

	result, err := importer.Import(ctx, agentimport.Source{Path: path})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s agent sync: failed: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}

	printAgentImportResult(result)
	if result.Skipped() {
		os.Exit(1)
	}
}

func definesSlug(defs []*agentpkg.Definition, slug string) bool {
	for _, d := range defs {
		if d != nil && d.Slug == slug {
			return true
		}
	}
	return false
}

func printAgentImportResult(result agentimport.Result) {
	created, synced, skipped := result.Counts()
	fmt.Printf("\n%s: %d created, %d synced, %d skipped\n", result.Path, created, synced, skipped)
	for _, o := range result.Outcomes {
		switch o.Action {
		case agentimport.ActionSkipped:
			fmt.Printf("  skipped  %s — %s\n", o.Slug, o.Reason)
		default:
			// Imported agents are read-only in place by design; say so
			// once here rather than leaving the operator to discover it
			// at the first failed edit.
			fmt.Printf("  %-8s %s (%s) — external provenance, read-only; copy to managed to edit\n",
				string(o.Action), o.Slug, o.Name)
		}
	}
}
