package service

import (
	"context"
	"log/slog"
	"time"

	chatstream "github.com/hollis-labs/libs/ui-go/chatstream"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/usagecost"
	costcalc "github.com/hollis-labs/substrate/llm-core/costcalc"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
	ledger "github.com/hollis-labs/substrate/llm-core/usageledger"
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

// Bound bookkeeping before stream cleanup even when the DB pool is held.
const usagePersistenceTimeout = 3 * time.Second

// persistRunUsage is best-effort outcome bookkeeping on the generation
// coordinator. One attempt serves normal finalization and the deferred fallback
// on early returns/panic unwinds; failure never replaces the turn's error.
func (s *chatServiceImpl) persistRunUsage(ctx context.Context, sessionID, messageID, model string, run *runState) {
	if run == nil || run.usagePersistenceAttempted || len(run.usageCalls) == 0 {
		return
	}
	// Mark before writing: an ambiguous write failure must not cause a second
	// insertion when the fallback runs. No other goroutine mutates this run.
	run.usagePersistenceAttempted = true
	usage := run.finalUsage
	if usage == nil {
		usage = &chat.Usage{}
	}
	toolInputTokens := 0
	if run.breakdown != nil {
		toolInputTokens = run.breakdown.Tools
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), usagePersistenceTimeout)
	defer cancel()
	if err := s.store.RecordUsageSnapshot(persistCtx, sessionID, messageID, model,
		usage.InputTokens, usage.OutputTokens, toolInputTokens,
		usage.CacheCreationTokens, usage.CacheReadTokens, run.usageCalls); err != nil {
		slog.Warn("chat-service: failed to record token usage", "session_id", sessionID, "message_id", messageID, "err", err)
	}
}

// Missing dimensions stay omitted; the ledger remains the source for provenance.
// Never reinterpret the old provider aggregate input as uncached input.
func canonicalRunUsage(rows []ledger.Row) *chatstream.Usage {
	if len(rows) == 0 {
		return nil
	}
	u := &chatstream.Usage{Scope: chatstream.UsageFinal}
	for _, row := range rows {
		if row.Usage.UncachedInputTokens.Provenance == ledger.ProvenanceUnknown || row.Usage.OutputTokens.Provenance == ledger.ProvenanceUnknown {
			return nil
		}
		u.UncachedInput += int(row.Usage.UncachedInputTokens.Tokens)
		u.CacheRead += int(row.Usage.CacheReadTokens.Tokens)
		u.CacheWrite += int(row.Usage.CacheWriteTokens.Tokens)
		u.Output += int(row.Usage.OutputTokens.Tokens)
		u.Reasoning += int(row.Usage.ReasoningTokens.Tokens)
	}
	return u
}
