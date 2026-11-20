// Command nanite-eval runs the Code Mode eval scenarios: each task once with
// direct tool use and once with the subtask tool, compared from the turns'
// trace records.
//
// Without -live it runs offline against each scenario's scripted model, the
// same run CI makes. With -live it calls the configured provider and spends
// money: each run is capped by -turn-tokens and the whole run by -max-usd,
// priced with -price-in, -price-out and the cache prices (conservative by
// default, so the cap stops early rather than late).
//
// The live run reads its provider key as nanite does: from NANITE_GATEWAY_URL
// and NANITE_GATEWAY_KEY_REF for a gateway, else the keychain entry
// keychain://nanite/provider/<provider>, else the provider's own environment
// variable when NANITE_PROVIDER_KEY_ENV=1.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/evals"
	"github.com/hollis-labs/nanite/internal/provider"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "nanite-eval:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("nanite-eval", flag.ContinueOnError)
	live := fs.Bool("live", false, "call the configured provider (spends money); without it the run is offline and scripted")
	providerName := fs.String("provider", "anthropic", "provider for -live")
	only := fs.String("scenario", "", "comma-separated scenarios to run (default all)")
	mode := fs.String("mode", "", "run only this mode: direct or subtask (default both)")
	out := fs.String("out", "", "write results.md and results.json into this directory instead of printing the table")
	maxUSD := fs.Float64("max-usd", 10, "stop the whole run once the estimated spend reaches this many dollars (0: no cap)")
	turnTokens := fs.Int("turn-tokens", 150000, "token cap for one run, its subtasks included (0: no cap)")
	price := evals.ConservativePrice
	fs.Float64Var(&price.Input, "price-in", price.Input, "dollars per million input tokens")
	fs.Float64Var(&price.Output, "price-out", price.Output, "dollars per million output tokens")
	fs.Float64Var(&price.CacheRead, "price-cache-read", price.CacheRead, "dollars per million cache-read tokens")
	fs.Float64Var(&price.CacheWrite, "price-cache-write", price.CacheWrite, "dollars per million cache-write tokens")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var names []string
	if *only != "" {
		names = strings.Split(*only, ",")
	}
	scenarios, err := evals.Find(names...)
	if err != nil {
		return err
	}
	modes := evals.Modes
	if *mode != "" {
		m := evals.Mode(*mode)
		if m != evals.Direct && m != evals.Subtask {
			return fmt.Errorf("unknown mode %q", *mode)
		}
		modes = []evals.Mode{m}
	}
	cfg := evals.Config{TurnTokens: *turnTokens, Price: price}
	if *live {
		cfg.MaxUSD = *maxUSD
		gw := agent.Gateway{BaseURL: os.Getenv("NANITE_GATEWAY_URL"), KeyRef: os.Getenv("NANITE_GATEWAY_KEY_REF"), Auth: os.Getenv("NANITE_GATEWAY_AUTH")}
		cfg.Gateway = gw
		model, err := provider.New(provider.Config{Provider: *providerName, Gateway: provider.Gateway{BaseURL: gw.BaseURL, KeyRef: gw.KeyRef, Auth: gw.Auth}})
		if err != nil {
			return err
		}
		cfg.Provider = func(evals.Scenario, evals.Mode) (llmcontracts.Provider, error) { return model, nil }
	}
	results, err := evals.Run(ctx, cfg, scenarios, modes)
	if err != nil {
		return err
	}
	md := evals.Markdown(results, true)
	if *out == "" {
		_, err = io.WriteString(w, md)
		return err
	}
	js, err := evals.JSON(results, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "results.md"), []byte(md), 0o644); err != nil { //nolint:gosec // results are meant to be shared
		return err
	}
	return os.WriteFile(filepath.Join(*out, "results.json"), js, 0o644) //nolint:gosec // results are meant to be shared
}
