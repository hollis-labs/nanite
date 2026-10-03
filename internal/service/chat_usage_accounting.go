package service

import (
	llmtypes "github.com/hollis-labs/go-llm-types"
	costcalc "github.com/hollis-labs/go-modelsdev-catalog-helpers"
	ledger "github.com/hollis-labs/go-usage-ledger"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/usagecost"
)

// Every streamed provider call, including post-loop synthesis/correction,
// captures its prices before dispatch and retains missing usage as PARTIAL.
type providerCallAccounting struct {
	row    ledger.Row
	report usagecost.Report
	run    *runState
}

func (s *chatServiceImpl) startUsageCall(run *runState, provider, model string) *providerCallAccounting {
	var catalog costcalc.Catalog
	if s.modelCatalog != nil {
		catalog = s.modelCatalog
	}
	return &providerCallAccounting{row: usagecost.NewRow(catalog, provider, model), run: run}
}

func (a *providerCallAccounting) consume(evt llmtypes.StreamEvent) {
	report, ok := usagecost.Parse(evt.Content)
	if !ok {
		report = usagecost.Fallback(evt.Usage)
	}
	a.report.Merge(report)
}

func (a *providerCallAccounting) finish() {
	if a.run == nil {
		return
	}
	a.report.Apply(&a.row)
	a.run.usageCalls = append(a.run.usageCalls, a.row)
}

// Supplemental calls emit incremental shared Usage just like the main stream.
func (a *providerCallAccounting) consumeSupplemental(evt llmtypes.StreamEvent) {
	a.consume(evt)
	if a.run == nil || evt.Usage == nil {
		return
	}
	if a.run.finalUsage == nil {
		a.run.finalUsage = &chat.Usage{}
	}
	if evt.Usage.InputTokens > 0 {
		a.run.finalUsage.InputTokens += evt.Usage.InputTokens
	}
	if evt.Usage.OutputTokens > 0 {
		a.run.finalUsage.OutputTokens += evt.Usage.OutputTokens
	}
	if evt.Usage.CacheReadTokens > 0 {
		a.run.finalUsage.CacheReadTokens += evt.Usage.CacheReadTokens
	}
	if evt.Usage.CacheCreationTokens > 0 {
		a.run.finalUsage.CacheCreationTokens += evt.Usage.CacheCreationTokens
	}
}
