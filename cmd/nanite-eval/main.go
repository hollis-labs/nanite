// Command nanite-eval runs the Code Mode eval scenarios: each task in several
// arms (direct tool use, the subtask tool, and subtask settings), compared from
// the turns' trace records.
//
// Without -live it runs offline against each scenario's scripted model, the
// same run CI makes: a harness check, not evidence. With -live it calls the
// configured provider and spends money. A live run needs a dollar cap
// (-max-usd, at most 50), a per-run token cap (-turn-tokens), prices of at
// least a cent per million tokens and a model name; every model request
// reserves its most expensive outcome against the cap before it is sent. It
// prints the provider, model and prices before it starts, and writes what ran
// and the spend so far even when it is interrupted or fails.
//
// The live run reads its provider key as nanite does: from NANITE_GATEWAY_URL
// and NANITE_GATEWAY_KEY_REF for a gateway, else the keychain entry
// keychain://nanite/provider/<provider>, else the provider's own environment
// variable when NANITE_PROVIDER_KEY_ENV=1.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/evals"
	"github.com/hollis-labs/nanite/internal/provider"
)

// maxCap is the most a single live run may be allowed to spend, and
// minPrice the lowest price per million tokens it accepts: a price near zero
// would make the cap meaningless.
const (
	maxCap   = 50.0
	minPrice = 0.01
)

// listPrices are list prices in dollars per million input and output tokens
// (Anthropic's model table, cached 2026-11-03). A configured price below one
// of them makes the cap stop late, so the run warns.
var listPrices = map[string][2]float64{
	"claude-fable-5-2":  {8, 40},
	"claude-opus-5-5":   {4, 20},
	"claude-sonnet-5-5": {2, 10},
	"claude-haiku-5-5":  {0.10, 0.50},
}

// priceWarning says, loudly, when a price is below the model's list price.
func priceWarning(model string, p evals.Price) string {
	list, ok := listPrices[model]
	if !ok {
		return fmt.Sprintf("WARNING: no list price is known for %s; check -price-in and -price-out are at least its real prices, or the dollar cap will stop late", model)
	}
	if p.Input < list[0] || p.Output < list[1] {
		return fmt.Sprintf("WARNING: the prices given ($%g in, $%g out per million tokens) are below %s's list prices ($%g in, $%g out): the cost estimates and the dollar cap will run low", p.Input, p.Output, model, list[0], list[1])
	}
	return ""
}

// modelName is what a -model may look like.
var modelName = regexp.MustCompile(`^[a-z0-9.-]+$`)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "nanite-eval:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, w, status io.Writer) error {
	fs := flag.NewFlagSet("nanite-eval", flag.ContinueOnError)
	live := fs.Bool("live", false, "call the configured provider (spends money); without it the run is offline and scripted")
	providerName := fs.String("provider", "anthropic", "provider for -live: anthropic or openai")
	model := fs.String("model", "claude-sonnet-5-5", "model for -live, written into every run's host.yaml (parent and subtasks)")
	only := fs.String("scenario", "", "comma-separated scenarios to run (default all)")
	mode := fs.String("mode", "", "run only the arms of this mode: direct or subtask (default all)")
	out := fs.String("out", "", "write results.md and results.json into this directory instead of printing the table")
	maxUSD := fs.Float64("max-usd", 10, "live: stop the whole run before its estimated spend passes this many dollars (0 < cap <= 50)")
	parallel := fs.Int("parallel", 1, "how many runs happen at once (1-8); they share the dollar cap")
	turnTokens := fs.Int("turn-tokens", 150000, "live: token cap for one run's task turn, its subtasks included (> 0)")
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
	if *parallel < 1 || *parallel > 8 {
		return fmt.Errorf("-parallel must be between 1 and 8, not %d", *parallel)
	}
	cfg := evals.Config{TurnTokens: *turnTokens, Price: price, Parallel: *parallel}
	if *mode != "" {
		m := evals.Mode(*mode)
		if m != evals.Direct && m != evals.Subtask {
			return fmt.Errorf("unknown mode %q", *mode)
		}
		cfg.Modes = []evals.Mode{m}
	}
	if *live {
		if err := checkLive(*providerName, *model, *maxUSD, *turnTokens, price); err != nil {
			return err
		}
		cfg.MaxUSD, cfg.Model = *maxUSD, *model
		gw := agent.Gateway{BaseURL: os.Getenv("NANITE_GATEWAY_URL"), KeyRef: os.Getenv("NANITE_GATEWAY_KEY_REF"), Auth: os.Getenv("NANITE_GATEWAY_AUTH")}
		cfg.Gateway = gw
		p, err := provider.New(provider.Config{Provider: *providerName, Gateway: provider.Gateway{BaseURL: gw.BaseURL, KeyRef: gw.KeyRef, Auth: gw.Auth}})
		if err != nil {
			return err
		}
		cfg.Provider = func(evals.Scenario, evals.Arm) (llmcontracts.Provider, error) { return p, nil }
		if status != nil {
			if w := priceWarning(*model, price); w != "" {
				fmt.Fprintln(status, "nanite-eval: "+w)
			}
			fmt.Fprintf(status, "nanite-eval: live run: provider %s, model %s; price per million tokens $%g in, $%g out, $%g cache read, $%g cache write; cap $%.2f total, %d tokens a run\n",
				*providerName, *model, price.Input, price.Output, price.CacheRead, price.CacheWrite, *maxUSD, *turnTokens)
		}
	}
	rep, runErr := evals.Run(ctx, cfg, scenarios)
	// Whatever ran is written, with the spend, even when the run stopped
	// early.
	if err := write(rep, *out, w); err != nil {
		return errors.Join(runErr, err)
	}
	if status != nil && *live {
		fmt.Fprintf(status, "nanite-eval: estimated spend $%.4f\n", rep.SpentUSD)
	}
	return runErr
}

// checkLive refuses a live run without bounds, or with a model that does not
// belong to the provider.
func checkLive(providerName, model string, maxUSD float64, turnTokens int, p evals.Price) error {
	if math.IsNaN(maxUSD) || math.IsInf(maxUSD, 0) || maxUSD <= 0 || maxUSD > maxCap {
		return fmt.Errorf("-max-usd must be more than 0 and at most %g for a live run", maxCap)
	}
	if turnTokens <= 0 {
		return errors.New("-turn-tokens must be more than 0 for a live run")
	}
	for name, v := range map[string]float64{"-price-in": p.Input, "-price-out": p.Output, "-price-cache-read": p.CacheRead, "-price-cache-write": p.CacheWrite} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < minPrice {
			return fmt.Errorf("%s must be at least %g for a live run", name, minPrice)
		}
	}
	if !modelName.MatchString(model) {
		return fmt.Errorf("model %q is not a model name (lower-case letters, digits, dots and dashes)", model)
	}
	claude := strings.HasPrefix(model, "claude-")
	switch providerName {
	case "anthropic":
		if !claude {
			return fmt.Errorf("model %q is not an Anthropic model", model)
		}
	case "openai":
		if claude {
			return fmt.Errorf("model %q is not an OpenAI model", model)
		}
	default:
		return fmt.Errorf("unknown provider %q", providerName)
	}
	return nil
}

func write(rep evals.Report, out string, w io.Writer) error {
	md := evals.Markdown(rep, true)
	if out == "" {
		_, err := io.WriteString(w, md)
		return err
	}
	js, err := evals.JSON(rep, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "results.md"), []byte(md), 0o644); err != nil { //nolint:gosec // results are meant to be shared
		return err
	}
	return os.WriteFile(filepath.Join(out, "results.json"), js, 0o644) //nolint:gosec // results are meant to be shared
}
