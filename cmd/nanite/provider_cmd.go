package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	secrets "github.com/hollis-labs/go-secrets"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdProvider dispatches `nanite provider <list|status|set-key|clear-key|set-ref>`.
// It reads and writes provider keys by reference and needs no running server.
func cmdProvider(args []string) {
	if len(args) == 0 {
		providerUsage(os.Stderr)
		os.Exit(2)
	}
	st, err := store.New(context.Background(), resolveDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s provider: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}

	// set-ref writes into the provider's row. A fresh install has none until
	// `serve` seeds them, so seed here too (idempotent, as at boot).
	if args[0] == "set-ref" {
		if err := st.SeedProviders(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "%s provider: seed providers: %v\n", brand.BinaryName, err)
			closeStoreBestEffort(context.Background(), st)
			os.Exit(1)
		}
	}
	keys := service.DefaultProviderKeys(st)
	stdin := os.Stdin
	code := runProvider(context.Background(), args, keys, stdin, stdinIsTerminal(stdin), os.Stdout, os.Stderr)
	closeStoreBestEffort(context.Background(), st)
	os.Exit(code)
}

func stdinIsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err != nil || fi.Mode()&os.ModeCharDevice != 0
}

func providerUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: %[1]s provider <command>

commands:
  list [--json]            API providers, whether each is usable, and where its key comes from
  status [<id>] [--json]   the same with the reason a provider is not usable; exits 1 if one is not
  set-key <id>             save a key read from stdin (never an argument): pbpaste | %[1]s provider set-key anthropic
  clear-key <id>           remove the saved key
  set-ref <id> <ref>       resolve this provider's key from a reference (keychain://, helper://, file://, op://)
  set-ref <id> --clear     go back to the default location

<id> is the provider id (anthropic-001) or its name (anthropic). Keys are never printed.
The environment (ANTHROPIC_API_KEY, ...) is read only when %[2]s=1.
Restart a running server, or use the UI, to pick up a change made here.
`, brand.BinaryName, providerkeys.EnvEnableVar)
}

// keyManager is the part of *providerkeys.Manager the command uses.
type keyManager interface {
	Status(ctx context.Context, providerID string) providerkeys.Status
	SetKey(ctx context.Context, providerID string, r io.Reader) error
	ClearKey(ctx context.Context, providerID string) error
	SetKeyRef(ctx context.Context, providerID, ref string) error
}

// providerTarget maps an id or a name to the provider row id of an API
// provider Nanite has an adapter for.
func providerTarget(arg string) (service.APIProviderSpec, error) {
	for _, spec := range service.APIProviderSpecs() {
		if spec.ProviderID == arg || spec.Name == arg {
			return spec, nil
		}
	}
	var names []string
	for _, spec := range service.APIProviderSpecs() {
		names = append(names, spec.Name)
	}
	return service.APIProviderSpec{}, fmt.Errorf("unknown provider %q; API providers: %s", arg, strings.Join(names, ", "))
}

// runProvider executes one provider subcommand and returns the exit status.
func runProvider(ctx context.Context, args []string, keys keyManager, stdin io.Reader, stdinTTY bool, out, errOut io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(errOut, "%s provider %s: %s\n", brand.BinaryName, args[0], fmt.Sprintf(format, a...))
		return 1
	}
	switch args[0] {
	case "list", "status":
		fs := flag.NewFlagSet("provider "+args[0], flag.ContinueOnError)
		fs.SetOutput(errOut)
		asJSON := fs.Bool("json", false, "machine-readable output")
		if err := fs.Parse(reorderFlags(args[1:])); err != nil {
			return 2
		}
		specs := service.APIProviderSpecs()
		if fs.NArg() > 1 {
			return fail("at most one provider")
		}
		if fs.NArg() == 1 {
			spec, err := providerTarget(fs.Arg(0))
			if err != nil {
				return fail("%v", err)
			}
			specs = []service.APIProviderSpec{spec}
		}
		var rows []providerkeys.Status
		allUsable := true
		for _, spec := range specs {
			st := keys.Status(ctx, spec.ProviderID)
			rows = append(rows, st)
			allUsable = allUsable && st.Usable
		}
		if *asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rows)
		} else {
			writeProviderTable(out, rows, args[0] == "status")
		}
		if args[0] == "status" && !allUsable {
			return 1
		}
		return 0

	case "set-key":
		if len(args) != 2 {
			return fail("usage: %s provider set-key <id>   (the key is read from stdin)", brand.BinaryName)
		}
		spec, err := providerTarget(args[1])
		if err != nil {
			return fail("%v", err)
		}
		if stdinTTY {
			return fail("the key is read from stdin, never from an argument or an echoing prompt; pipe it in, e.g. pbpaste | %s provider set-key %s", brand.BinaryName, spec.Name)
		}
		if err := keys.SetKey(ctx, spec.ProviderID, stdin); err != nil {
			return fail("%v", describeKeyError(err))
		}
		st := keys.Status(ctx, spec.ProviderID)
		if !st.Usable {
			return fail("the key was written but %s still cannot resolve it (%s): %s", spec.Name, st.Code, st.Guidance)
		}
		fmt.Fprintf(out, "%s: key saved; resolves from %s\n", spec.Name, st.Source)
		return 0

	case "clear-key":
		if len(args) != 2 {
			return fail("usage: %s provider clear-key <id>", brand.BinaryName)
		}
		spec, err := providerTarget(args[1])
		if err != nil {
			return fail("%v", err)
		}
		if err := keys.ClearKey(ctx, spec.ProviderID); err != nil {
			return fail("%v", describeKeyError(err))
		}
		st := keys.Status(ctx, spec.ProviderID)
		if st.Usable {
			fmt.Fprintf(out, "%s: saved key removed; the provider still resolves a key from %s\n", spec.Name, st.Source)
		} else {
			fmt.Fprintf(out, "%s: saved key removed\n", spec.Name)
		}
		return 0

	case "set-ref":
		fs := flag.NewFlagSet("provider set-ref", flag.ContinueOnError)
		fs.SetOutput(errOut)
		clearRef := fs.Bool("clear", false, "remove the configured reference")
		if err := fs.Parse(reorderFlags(args[1:])); err != nil {
			return 2
		}
		want := 2
		if *clearRef {
			want = 1
		}
		if fs.NArg() != want {
			return fail("usage: %s provider set-ref <id> <ref> | --clear", brand.BinaryName)
		}
		spec, err := providerTarget(fs.Arg(0))
		if err != nil {
			return fail("%v", err)
		}
		ref := ""
		if !*clearRef {
			ref = fs.Arg(1)
		}
		if err := keys.SetKeyRef(ctx, spec.ProviderID, ref); err != nil {
			return fail("%v", describeKeyError(err))
		}
		st := keys.Status(ctx, spec.ProviderID)
		if st.Usable {
			fmt.Fprintf(out, "%s: key_ref updated; resolves from %s\n", spec.Name, st.Source)
		} else {
			fmt.Fprintf(out, "%s: key_ref updated; not usable yet (%s): %s\n", spec.Name, st.Code, st.Guidance)
		}
		return 0

	default:
		providerUsage(errOut)
		return 2
	}
}

// describeKeyError renders a key error for the terminal: the typed message and
// remedy when there is one, never a backend's raw text.
func describeKeyError(err error) error {
	var ke *providerkeys.Error
	if errors.As(err, &ke) {
		return fmt.Errorf("%s: %s", ke.Code(), ke.Guidance())
	}
	var se *secrets.Error
	if errors.As(err, &se) {
		return fmt.Errorf("%s: %s", se.Code(), se.Guidance())
	}
	return err
}

// reorderFlags moves flags ahead of positional arguments so
// `provider status anthropic --json` parses like `--json anthropic`.
func reorderFlags(args []string) []string {
	var flags, pos []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			pos = append(pos, a)
		}
	}
	return append(flags, pos...)
}

func writeProviderTable(out io.Writer, rows []providerkeys.Status, detail bool) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tUSABLE\tSOURCE\tREFERENCE")
	for _, r := range rows {
		usable, source := "no", "-"
		if r.Usable {
			usable, source = "yes", r.Source
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ProviderID, usable, source, r.Ref)
	}
	_ = tw.Flush()
	if !detail {
		return
	}
	for _, r := range rows {
		if !r.Usable {
			fmt.Fprintf(out, "\n%s: %s\n  %s\n", r.ProviderID, r.Code, r.Guidance)
		}
	}
}
