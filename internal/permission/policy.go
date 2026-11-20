package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/hollis-labs/libs/security/policy"
	rules "github.com/hollis-labs/libs/security/policy/permission"
	"gopkg.in/yaml.v3"
)

// RulePDP evaluates the authored tool rules and exposes their effective mode.
type RulePDP struct{ engine *rules.Engine }

func (p RulePDP) Evaluate(ctx context.Context, req policy.Request) (policy.Decision, error) {
	input, _ := req.Context["input"].(map[string]any)
	readOnly, _ := req.Context["read_only"].(bool)
	destructive, _ := req.Context["destructive"].(bool)
	check := p.engine.Check(ctx, req.Scope, req.Resource.ID, input, rules.ToolMeta{IsReadOnly: readOnly, IsDestructive: destructive})
	var effect policy.Effect
	switch check.Decision {
	case rules.DecisionAllow:
		effect = policy.Allow
		if p.engine.Mode() == rules.ModeDefault && isEdit(req.Resource.ID) && check.MatchedRule == nil {
			effect = policy.Ask
			check.Reason = "default mode requires approval for file edits"
		}
	case rules.DecisionAsk:
		effect = policy.Ask
	case rules.DecisionDeny:
		effect = policy.Deny
	default:
		return policy.Decision{Effect: policy.Deny, Reason: "unrecognized permission decision"}, nil
	}
	return policy.Decision{Effect: effect, Reason: check.Reason}, nil
}

func (p RulePDP) Mode() string { return string(p.engine.Mode()) }

func Rules(mode string, data []byte) (*RulePDP, error) {
	var file rules.PermissionsFile
	if len(bytes.TrimSpace(data)) > 0 {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&file); err != nil {
			return nil, fmt.Errorf("permissions.yaml: %w", err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("permissions.yaml must contain one document")
		}
	}
	if err := file.Permissions.Validate(); err != nil {
		return nil, err
	}
	if mode == "" {
		mode = string(file.Permissions.Mode)
	}
	if mode == "" {
		mode = string(rules.ModeDefault)
	}
	return &RulePDP{rules.NewEngine(rules.Mode(mode), &file.Permissions, rules.WithFileEditTools("write_file", "edit_file"))}, nil
}

// Evaluate decides one tool call. extra is added to the request context; a
// subtask passes its variant, child conversation and depth there, so a host
// PDP may narrow a subtask further.
func Evaluate(ctx context.Context, logger *slog.Logger, outer, local policy.PDP, principal, role string, req PermissionRequest, args, extra map[string]any) (policy.Decision, error) {
	values := map[string]any{"input": args, "read_only": req.ReadOnly, "destructive": req.Destructive, "conversation": req.ConversationID, "role": role}
	for k, v := range extra {
		if _, taken := values[k]; !taken {
			values[k] = v
		}
	}
	input := policy.Request{RequestID: req.ID, Principal: policy.Principal{URN: principal, Kind: "agent"},
		Action: "tool.call", Scope: req.ConversationID, Resource: policy.Resource{Type: "tool", ID: req.Tool},
		Context: values}
	decision := policy.Decision{Effect: policy.Allow}
	for _, pdp := range []policy.PDP{outer, local} {
		if pdp == nil {
			continue
		}
		evaluator := policy.Evaluator{PDP: pdp, Audit: auditLog{logger}}
		res, err := evaluator.Evaluate(ctx, input, policy.Options{Mode: policy.Enforce, FailMode: policy.FailClosed})
		if err != nil {
			return policy.Decision{Effect: policy.Deny, Reason: "policy evaluation failed"}, err
		}
		if len(res.Decision.Obligations) > 0 {
			return policy.Decision{Effect: policy.Deny, Reason: "policy obligations require enforcement by the embedding host"}, nil
		}
		if res.Decision.Effect == policy.Deny {
			return res.Decision, nil
		}
		if res.Decision.Effect == policy.Ask || decision.Effect == policy.Allow {
			decision = res.Decision
		}
	}
	return decision, nil
}

func Args(raw json.RawMessage) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	return args, nil
}

type auditLog struct{ logger *slog.Logger }

func (a auditLog) Record(ctx context.Context, event policy.AuditEvent) error {
	a.logger.DebugContext(ctx, "tool policy decision", "tool", event.Resource.ID, "conversation", event.Scope,
		"decision", string(event.Decision.Effect), "failure", string(event.Failure))
	return nil
}

func isEdit(name string) bool { return name == "write_file" || name == "edit_file" }
