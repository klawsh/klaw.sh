package agent

import "fmt"

// CostConfig controls budget enforcement.
type CostConfig struct {
	MaxSessionCost float64 // 0 = unlimited
	WarnThreshold  float64 // fraction of budget that triggers a warning (e.g. 0.8)
}

// ModelCost holds per-million-token pricing for a model.
type ModelCost struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// CostTracker tracks session cost and enforces budgets.
type CostTracker struct {
	config      CostConfig
	sessionCost float64
	totalInput  int
	totalOutput int
	costTable   map[string]ModelCost
}

// DefaultCostTable returns known model pricing (per million tokens).
func DefaultCostTable() map[string]ModelCost {
	return map[string]ModelCost{
		// Anthropic direct
		"claude-sonnet-4-20250514":     {InputPerMillion: 3.0, OutputPerMillion: 15.0},
		"claude-opus-4-20250514":       {InputPerMillion: 15.0, OutputPerMillion: 75.0},
		"claude-3-5-sonnet-20241022":   {InputPerMillion: 3.0, OutputPerMillion: 15.0},
		"claude-3-5-haiku-20241022":    {InputPerMillion: 0.80, OutputPerMillion: 4.0},
		// OpenRouter / EachLabs prefixed
		"anthropic/claude-sonnet-4":    {InputPerMillion: 3.0, OutputPerMillion: 15.0},
		"anthropic/claude-sonnet-4-5":  {InputPerMillion: 3.0, OutputPerMillion: 15.0},
		"anthropic/claude-opus-4":      {InputPerMillion: 15.0, OutputPerMillion: 75.0},
		"openai/gpt-4o":               {InputPerMillion: 2.5, OutputPerMillion: 10.0},
		"openai/gpt-4o-mini":          {InputPerMillion: 0.15, OutputPerMillion: 0.60},
		"google/gemini-2.0-flash-exp": {InputPerMillion: 0.10, OutputPerMillion: 0.40},
		"deepseek/deepseek-chat":      {InputPerMillion: 0.14, OutputPerMillion: 0.28},
	}
}

// NewCostTracker creates a cost tracker.
func NewCostTracker(cfg CostConfig) *CostTracker {
	return &CostTracker{
		config:    cfg,
		costTable: DefaultCostTable(),
	}
}

// Record adds a usage record and returns the incremental cost.
func (ct *CostTracker) Record(model string, input, output int) float64 {
	ct.totalInput += input
	ct.totalOutput += output

	pricing, ok := ct.costTable[model]
	if !ok {
		// Unknown model — use a conservative estimate
		pricing = ModelCost{InputPerMillion: 3.0, OutputPerMillion: 15.0}
	}

	cost := float64(input)/1_000_000*pricing.InputPerMillion +
		float64(output)/1_000_000*pricing.OutputPerMillion
	ct.sessionCost += cost
	return cost
}

// CheckBudget returns an error if the session cost exceeds the budget.
func (ct *CostTracker) CheckBudget() error {
	if ct.config.MaxSessionCost <= 0 {
		return nil
	}
	if ct.sessionCost >= ct.config.MaxSessionCost {
		return &AgentError{
			Code:    ErrBudgetExceed,
			Message: fmt.Sprintf("session cost $%.4f exceeds budget $%.2f", ct.sessionCost, ct.config.MaxSessionCost),
		}
	}
	return nil
}

// IsNearBudget returns true if cost has passed the warning threshold.
func (ct *CostTracker) IsNearBudget() bool {
	if ct.config.MaxSessionCost <= 0 || ct.config.WarnThreshold <= 0 {
		return false
	}
	return ct.sessionCost >= ct.config.MaxSessionCost*ct.config.WarnThreshold
}

// SessionCost returns the total session cost so far.
func (ct *CostTracker) SessionCost() float64 {
	return ct.sessionCost
}

// Summary returns a human-readable cost summary.
func (ct *CostTracker) Summary() string {
	inK := float64(ct.totalInput) / 1000
	outK := float64(ct.totalOutput) / 1000
	return fmt.Sprintf("$%.4f (%.1fk in / %.1fk out)", ct.sessionCost, inK, outK)
}
