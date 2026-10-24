package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

const agentEnrollUsage = "usage: nanite agent enroll <host-settings-id> --reason R [--revision REV]"

// enrollCLIActor is the accountable principal recorded for an enrollment made
// from the offline CLI: whoever can write the database is the operator, as for
// `nanite agent setup`.
const enrollCLIActor = "cli:agent-enroll"

// runAgentEnroll is `nanite agent enroll`: give an agent a durable Tether actor.
// It needs NANITE_TETHER_ADDR and a tether.registry token in the 0600 file named
// by NANITE_TETHER_REGISTRY_TOKEN_FILE; neither is a flag, so no credential can
// land in shell history. Retiring an enrolled agent is host_settings_retire
// (HTTP or MCP), which also deregisters the actor; there is no CLI unenroll in v1.
func runAgentEnroll(ctx context.Context, svc *service.EnrollmentService, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agent enroll", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	reason := fs.String("reason", "", "why this agent is enrolled (recorded)")
	revision := fs.String("revision", "", "host settings revision you reviewed (optional)")
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%w\n%s", err, agentEnrollUsage)
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) != 1 {
		return errors.New("one host settings id is required\n" + agentEnrollUsage)
	}
	res, err := svc.Enroll(ctx, enrollCLIActor, positional[0], *reason, *revision)
	if err != nil {
		return enrollCLIError(err)
	}
	return json.NewEncoder(out).Encode(res)
}

// enrollCLIError adds the next step to the typed errors an operator can fix.
func enrollCLIError(err error) error {
	if errors.Is(err, service.ErrEnrollmentReasonRequired) {
		return fmt.Errorf("%w\n%s", err, agentEnrollUsage)
	}
	if errors.Is(err, store.ErrEnrollmentNotFound) {
		return fmt.Errorf("host settings not found: %w", err)
	}
	return err
}
