package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"text/template"
	"time"

	pluginsdk "github.com/hollis-labs/fragments-engine/plugin"
)

// TriggerDispatcher evaluates trigger rules after events fire and calls
// the matching connector with a rendered payload. It also handles retry
// with exponential backoff.
type TriggerDispatcher struct {
	host   *Host
	logger pluginsdk.Logger

	// Retry configuration
	MaxRetries     int           // default 3
	InitialBackoff time.Duration // default 1s
	MaxBackoff     time.Duration // default 30s
	BackoffFactor  float64       // default 2.0
}

// NewTriggerDispatcher creates a dispatcher bound to a Host.
func NewTriggerDispatcher(host *Host) *TriggerDispatcher {
	return &TriggerDispatcher{
		host:           host,
		logger:         host.logger.With("component", "trigger-dispatch"),
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
	}
}

// Dispatch looks up enabled trigger rules for the given event and calls each
// matching connector. Fully asynchronous — callers never wait for connectors.
func (td *TriggerDispatcher) Dispatch(event pluginsdk.Event) {
	if td.host.store == nil {
		return
	}

	rules, err := td.host.store.ListTriggerRulesByEvent(event.Type)
	if err != nil {
		td.logger.Error("failed to load trigger rules", "eventType", event.Type, "error", err)
		return
	}
	if len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		// Check filter expression if set.
		if rule.FilterExpr != "" && !td.matchFilter(rule.FilterExpr, event.Data) {
			continue
		}

		// Resolve the connector.
		connector, ok := td.host.GetConnector(rule.ConnectorName)
		if !ok {
			td.logger.Warn("trigger rule references unknown connector",
				"ruleID", rule.ID, "connector", rule.ConnectorName)
			continue
		}

		// Render the payload template.
		payload, err := td.renderPayload(rule.PayloadTemplate, event)
		if err != nil {
			td.logger.Error("trigger payload render failed",
				"ruleID", rule.ID, "error", err)
			continue
		}

		go td.sendWithRetry(connector, payload, rule.ID)
	}
}

// matchFilter evaluates a simple filter expression against event data.
// Format: "field=value" or "field=value,field2=value2" (all must match).
// Empty filter always matches.
func (td *TriggerDispatcher) matchFilter(expr string, data map[string]interface{}) bool {
	pairs := strings.Split(expr, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			td.logger.Warn("invalid filter expression", "pair", pair)
			return false
		}
		key := strings.TrimSpace(parts[0])
		expected := strings.TrimSpace(parts[1])

		actual, ok := data[key]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", actual) != expected {
			return false
		}
	}
	return true
}

// renderPayload renders a JSON payload template with event data.
// The template uses Go text/template syntax: {{.Type}}, {{.SessionID}},
// {{.Data.field_name}}, etc.
func (td *TriggerDispatcher) renderPayload(tmplStr string, event pluginsdk.Event) (map[string]interface{}, error) {
	// If the template is empty or just "{}", pass through the event data as-is.
	trimmed := strings.TrimSpace(tmplStr)
	if trimmed == "" || trimmed == "{}" {
		return event.Data, nil
	}

	tmpl, err := template.New("payload").Option("missingkey=zero").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parse payload template: %w", err)
	}

	// Build the template context from the event.
	ctx := map[string]interface{}{
		"Type":      event.Type,
		"Source":    event.Source,
		"Timestamp": event.Timestamp.Format(time.RFC3339),
		"SessionID": event.SessionID,
		"Data":      event.Data,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("execute payload template: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		return nil, fmt.Errorf("payload template produced invalid JSON: %w", err)
	}
	return payload, nil
}

// sendWithRetry calls connector.Send with exponential backoff on failure.
func (td *TriggerDispatcher) sendWithRetry(connector pluginsdk.Connector, payload map[string]interface{}, ruleID string) {
	connName := connector.Name()
	for attempt := 0; attempt <= td.MaxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(td.host.ctx, 30*time.Second)
		err := connector.Send(ctx, payload)
		cancel()

		if err == nil {
			td.host.recordConnectorSuccess(connName)
			if attempt > 0 {
				td.logger.Info("connector send succeeded after retry",
					"connector", connName, "ruleID", ruleID, "attempt", attempt)
			}
			return
		}

		td.logger.Warn("connector send failed",
			"connector", connName, "ruleID", ruleID, "attempt", attempt, "error", err)

		if attempt < td.MaxRetries {
			backoff := td.calculateBackoff(attempt)
			select {
			case <-td.host.ctx.Done():
				td.logger.Info("trigger retry cancelled", "connector", connName, "ruleID", ruleID)
				return
			case <-time.After(backoff):
			}
		}
	}

	td.logger.Error("connector send exhausted retries",
		"connector", connName, "ruleID", ruleID, "maxRetries", td.MaxRetries)

	// Update connector health status.
	td.host.recordConnectorFailure(connName)
}

// calculateBackoff returns the backoff duration for a given attempt using
// exponential backoff with jitter capped at MaxBackoff.
func (td *TriggerDispatcher) calculateBackoff(attempt int) time.Duration {
	backoff := float64(td.InitialBackoff) * math.Pow(td.BackoffFactor, float64(attempt))
	if backoff > float64(td.MaxBackoff) {
		backoff = float64(td.MaxBackoff)
	}
	return time.Duration(backoff)
}
