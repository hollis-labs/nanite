package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/hollis-labs/nanite/internal/agentdir"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

const agentLoadUsage = "usage: nanite agent load [<dir>] [--dry-run] [--force] [--grant-same-as <host-slug-or-id>] [--reason R]"

// loadCLIActor is the accountable actor recorded for grants made from the
// offline CLI: whoever can write the database is the operator.
const loadCLIActor = "cli:agent-load"

// runAgentLoad is `nanite agent load`: read an agent directory and publish its
// current state as a new definition revision and host-settings row. It writes
// the database directly, like setup and install, so a running server picks the
// new row up for its next session without a restart; sessions already running
// keep the row they started under.
func runAgentLoad(ctx context.Context, st *store.Store, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agent load", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "show what would change and write nothing")
	force := fs.Bool("force", false, "publish a new revision of a pinned agent")
	grantSame := fs.String("grant-same-as", "", "re-grant the tools of this host (slug or id) to the new revision")
	reason := fs.String("reason", "", "why the tools are re-granted (recorded)")
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%w\n%s", err, agentLoadUsage)
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) > 1 {
		return errors.New("at most one directory\n" + agentLoadUsage)
	}
	arg := ""
	if len(positional) == 1 {
		arg = positional[0]
	}
	path, err := agentdir.Discover(arg)
	if err != nil {
		return err
	}
	dir, err := agentdir.Load(path)
	if err != nil {
		return err
	}
	svc := &service.AgentLoadService{Store: st}
	res, err := svc.Load(ctx, dir, service.AgentLoadOptions{Force: *force, DryRun: *dryRun, GrantFrom: *grantSame, Reason: *reason}, loadCLIActor)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}
