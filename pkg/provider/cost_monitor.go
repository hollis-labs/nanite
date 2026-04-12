package provider

import (
	"fmt"
	"sync"

	"github.com/hollis-labs/nanite/pkg/models"
)

// BudgetViolation represents a detected budget violation.
type BudgetViolation struct {
	Type        string  // "token_budget", "cost_budget"
	Description string
	Current     float64 // Current usage
	Limit       float64 // Budget limit
	Event       StreamEvent
}

func (bv *BudgetViolation) Error() string {
	return fmt.Sprintf("Budget violation (%s): %s (current: %.2f, limit: %.2f)",
		bv.Type, bv.Description, bv.Current, bv.Limit)
}

// CostMonitor tracks token usage and cost to ensure operations stay within
// budget. Pricing is drawn from pkg/models — the same source of truth used by
// internal/store.estimateCost — so budget enforcement and usage reporting
// agree on rates per-model. The legacy provider-keyed costRates map is kept
// only for backwards-compat overrides via SetCostRate.
type CostMonitor struct {
	tokenBudget        int     // Maximum tokens allowed
	costBudgetUSD      float64 // Maximum cost in USD
	budgetExceededMode string  // "log" or "kill"

	// Default model used when the caller does not pass a model ID through
	// the event pipeline. Falls back to models.DefaultChatModel().
	defaultModel string

	// Tracking state
	mu                sync.RWMutex
	totalInputTokens  int
	totalOutputTokens int
	totalCostUSD      float64

	// Legacy per-provider override map. Only consulted if the model is not
	// in the canonical registry.
	costRates map[string]CostRate
}

// CostRate defines the cost structure for a provider.
type CostRate struct {
	InputTokensPerDollar  float64 // How many input tokens per USD
	OutputTokensPerDollar float64 // How many output tokens per USD
	Name                  string  // Provider name for logging
}

// NewCostMonitor creates a new cost monitor with the given budget limits.
func NewCostMonitor(tokenBudget int, costBudgetUSD float64, budgetExceededMode string) *CostMonitor {
	cm := &CostMonitor{
		tokenBudget:        tokenBudget,
		costBudgetUSD:      costBudgetUSD,
		budgetExceededMode: budgetExceededMode,
		defaultModel:       models.DefaultChatModel(),
		costRates:          map[string]CostRate{},
	}
	return cm
}

// SetDefaultModel changes the model ID used for cost estimation when the
// event pipeline does not attach one. Useful for tests and for sessions
// scoped to a non-default model.
func (cm *CostMonitor) SetDefaultModel(model string) {
	cm.mu.Lock()
	cm.defaultModel = model
	cm.mu.Unlock()
}

// CheckEvent examines a stream event for budget violations.
func (cm *CostMonitor) CheckEvent(event StreamEvent) *BudgetViolation {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Update usage counters based on event type
	switch event.Type {
	case "usage", "done":
		if event.Usage != nil {
			return cm.updateUsageAndCheck(event)
		}
	}

	return nil
}

// updateUsageAndCheck updates usage counters and checks for budget violations.
func (cm *CostMonitor) updateUsageAndCheck(event StreamEvent) *BudgetViolation {
	usage := event.Usage

	// Update token counters
	cm.totalInputTokens += usage.InputTokens
	cm.totalOutputTokens += usage.OutputTokens

	// Use per-model pricing from the canonical registry. Previous versions
	// hardcoded "anthropic" here which overestimated cost for every other
	// provider (see audit 03). The event carries no explicit model today,
	// so the monitor's defaultModel is used — sessions that need precise
	// cost tracking should call SetDefaultModel from the chat service.
	estimatedCost := cm.estimateCostForModel(usage.InputTokens, usage.OutputTokens, cm.defaultModel)
	cm.totalCostUSD += estimatedCost

	// Check token budget
	totalTokens := cm.totalInputTokens + cm.totalOutputTokens
	if cm.tokenBudget > 0 && totalTokens > cm.tokenBudget {
		return &BudgetViolation{
			Type:        "token_budget",
			Description: fmt.Sprintf("Token budget exceeded: %d/%d tokens", totalTokens, cm.tokenBudget),
			Current:     float64(totalTokens),
			Limit:       float64(cm.tokenBudget),
			Event:       event,
		}
	}

	// Check cost budget
	if cm.costBudgetUSD > 0 && cm.totalCostUSD > cm.costBudgetUSD {
		return &BudgetViolation{
			Type:        "cost_budget",
			Description: fmt.Sprintf("Cost budget exceeded: $%.4f/$%.2f", cm.totalCostUSD, cm.costBudgetUSD),
			Current:     cm.totalCostUSD,
			Limit:       cm.costBudgetUSD,
			Event:       event,
		}
	}

	return nil
}

// estimateCostForModel calculates the estimated cost for the given token
// usage using per-model pricing from the canonical registry. Legacy
// provider-keyed overrides (set via SetCostRate) are consulted only if the
// model is not in the registry.
func (cm *CostMonitor) estimateCostForModel(inputTokens, outputTokens int, model string) float64 {
	if inP, outP := models.Pricing(model); inP > 0 || outP > 0 {
		return float64(inputTokens)*inP/1_000_000 + float64(outputTokens)*outP/1_000_000
	}
	// Fall back to a legacy per-provider override, keyed by provider type.
	providerType := models.ProviderFor(model)
	if providerType == "" {
		providerType = models.DefaultProvider()
	}
	if rate, ok := cm.costRates[providerType]; ok && rate.InputTokensPerDollar > 0 && rate.OutputTokensPerDollar > 0 {
		return float64(inputTokens)/rate.InputTokensPerDollar +
			float64(outputTokens)/rate.OutputTokensPerDollar
	}
	return 0
}

// GetUsageSummary returns a summary of current usage and costs.
func (cm *CostMonitor) GetUsageSummary() UsageSummary {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return UsageSummary{
		TotalInputTokens:  cm.totalInputTokens,
		TotalOutputTokens: cm.totalOutputTokens,
		TotalTokens:       cm.totalInputTokens + cm.totalOutputTokens,
		TotalCostUSD:      cm.totalCostUSD,
		TokenBudget:       cm.tokenBudget,
		CostBudgetUSD:     cm.costBudgetUSD,
		TokenUtilization:  cm.calculateTokenUtilization(),
		CostUtilization:   cm.calculateCostUtilization(),
	}
}

// UsageSummary provides a summary of current usage and budget status.
type UsageSummary struct {
	TotalInputTokens  int
	TotalOutputTokens int
	TotalTokens       int
	TotalCostUSD      float64
	TokenBudget       int
	CostBudgetUSD     float64
	TokenUtilization  float64 // Percentage of token budget used
	CostUtilization   float64 // Percentage of cost budget used
}

// calculateTokenUtilization returns the percentage of token budget used.
func (cm *CostMonitor) calculateTokenUtilization() float64 {
	if cm.tokenBudget <= 0 {
		return 0.0
	}
	totalTokens := cm.totalInputTokens + cm.totalOutputTokens
	return (float64(totalTokens) / float64(cm.tokenBudget)) * 100.0
}

// calculateCostUtilization returns the percentage of cost budget used.
func (cm *CostMonitor) calculateCostUtilization() float64 {
	if cm.costBudgetUSD <= 0 {
		return 0.0
	}
	return (cm.totalCostUSD / cm.costBudgetUSD) * 100.0
}

// Reset resets all usage counters to zero.
func (cm *CostMonitor) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.totalInputTokens = 0
	cm.totalOutputTokens = 0
	cm.totalCostUSD = 0.0
}

// SetCostRate allows updating cost rates for specific providers.
func (cm *CostMonitor) SetCostRate(provider string, rate CostRate) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.costRates[provider] = rate
}