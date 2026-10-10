// Package agentpolicy negotiates Nanite's intrinsic agentdef extensions.
// Definitions request behavior; they never supply host grants or identities.
package agentpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/hollis-labs/substrate/mesh/agentdef"
)

const (
	NativeNamespace = "com.hollislabs.nanite/native-policy"
	ReflexNamespace = "com.hollislabs.nanite/reflex-policy"
	Version         = "1"
)

type AutoRecall struct {
	Enabled       *bool    `json:"enabled,omitempty"`
	MinConfidence *float64 `json:"min_confidence,omitempty"`
}

// NativePolicy contains behavior only. Numeric execution limits, model
// selection, spawn admission, tool grants and trust remain host-owned.
type NativePolicy struct {
	Class                    string      `json:"class,omitempty"`
	SubagentCompletionPolicy string      `json:"subagent_completion_policy,omitempty"`
	MessageWakePolicy        string      `json:"message_wake_policy,omitempty"`
	WriteClaimGuard          string      `json:"write_claim_guard,omitempty"`
	AutoRecall               *AutoRecall `json:"auto_recall,omitempty"`
}

// Defaults retain native completion/wake/recall behavior. The host may enforce
// a stronger write guard; this request cannot weaken host policy.
func (p NativePolicy) Defaults() NativePolicy {
	if p.Class == "" {
		p.Class = "advisor"
	}
	if p.SubagentCompletionPolicy == "" {
		p.SubagentCompletionPolicy = "render_and_wait"
	}
	if p.MessageWakePolicy == "" {
		p.MessageWakePolicy = "auto_summarize"
	}
	if p.WriteClaimGuard == "" {
		p.WriteClaimGuard = "deny"
	}
	if p.AutoRecall == nil {
		p.AutoRecall = &AutoRecall{}
	} else {
		c := *p.AutoRecall
		p.AutoRecall = &c
	}
	if p.AutoRecall.Enabled == nil {
		v := true
		p.AutoRecall.Enabled = &v
	}
	if p.AutoRecall.MinConfidence == nil {
		v := .4
		p.AutoRecall.MinConfidence = &v
	}
	return p
}

func (p NativePolicy) Validate() error {
	if !oneOf(p.Class, "", "advisor", "process") {
		return fmt.Errorf("unsupported native cognitive class %q", p.Class)
	}
	for _, policy := range []string{p.SubagentCompletionPolicy, p.MessageWakePolicy} {
		if !oneOf(policy, "", "render_and_wait", "auto_summarize", "batch") {
			return fmt.Errorf("unsupported completion policy %q", policy)
		}
	}
	if !oneOf(p.WriteClaimGuard, "", "off", "warn", "ask", "deny") {
		return fmt.Errorf("unsupported write claim guard %q", p.WriteClaimGuard)
	}
	if p.AutoRecall != nil && p.AutoRecall.MinConfidence != nil {
		v := *p.AutoRecall.MinConfidence
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return errors.New("auto_recall.min_confidence must be finite and between zero and one")
		}
	}
	return nil
}

type ReflexPolicy struct {
	Bundle agentdef.Ref `json:"bundle"`
}

// Option supplies validators for the agreed version and area. Unknown versions
// remain unnegotiated, so mandatory behavior is refused by agentdef itself.
func Option() agentdef.Option {
	return agentdef.WithExtensions(func(namespace, version string) (func(agentdef.Extension) error, bool) {
		if version != Version {
			return nil, false
		}
		switch namespace {
		case NativeNamespace:
			return func(e agentdef.Extension) error { _, err := DecodeNative(e); return err }, true
		case ReflexNamespace:
			return func(e agentdef.Extension) error { _, err := DecodeReflex(e); return err }, true
		default:
			return nil, false
		}
	})
}

func DecodeNative(e agentdef.Extension) (NativePolicy, error) {
	var p NativePolicy
	if e.Version != Version || e.Area != "harness_profile" {
		return p, errors.New("native-policy requires version 1 and area harness_profile")
	}
	if err := decodeData(e.Data, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

func DecodeReflex(e agentdef.Extension) (ReflexPolicy, error) {
	var p ReflexPolicy
	if e.Version != Version || e.Area != "behavior" {
		return p, errors.New("reflex-policy requires version 1 and area behavior")
	}
	if err := decodeData(e.Data, &p); err != nil {
		return p, err
	}
	// Use the core validator's Ref contract rather than another digest grammar.
	d := &agentdef.Definition{SchemaVersion: "2", DefinitionID: "ref-check", Revision: "1", Name: "ref-check", Description: "Ref validation", Body: "Ref validation", Behavior: agentdef.Behavior{Purpose: "Validate ref", Instructions: []agentdef.Ref{p.Bundle}}, HarnessProfile: agentdef.HarnessProfile{Permissions: agentdef.PermissionProfile{Profile: "default"}}, Continuity: agentdef.Continuity{Mode: agentdef.Ephemeral}}
	return p, d.Validate()
}

func decodeData(data map[string]any, target any) error {
	if data == nil {
		return errors.New("extension data must be an object")
	}
	if err := nonNull(data); err != nil {
		return err
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return decode(b, target)
}

func nonNull(value any) error {
	switch v := value.(type) {
	case nil:
		return errors.New("explicit null is not supported in policy data")
	case map[string]any:
		for _, child := range v {
			if err := nonNull(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := nonNull(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func decode(data []byte, target any) error {
	check := json.NewDecoder(bytes.NewReader(data))
	if err := checkObject(check, 0); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return errors.New("exactly one JSON document is required")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return errors.New("exactly one JSON document is required")
	}
	return nil
}

// DecodeJSON rejects ambiguous host or authoring inputs before decoding them.
// Omitted fields may use their documented defaults; explicit null, repeated
// fields, unknown fields and additional documents are never replacements.
func DecodeJSON(data []byte, target any) error { return decode(data, target) }

// Bundle JSON receives the same ambiguity refusal as agentdef YAML: neither
// repeated keys nor null can silently replace declared behavior with defaults.
func checkObject(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("policy JSON nesting exceeds 64")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("explicit null is not supported in policy data")
	}
	d, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, keyErr := dec.Token()
			if keyErr != nil {
				return keyErr
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("policy JSON object has duplicate or invalid key")
			}
			seen[name] = true
			if childErr := checkObject(dec, depth+1); childErr != nil {
				return childErr
			}
		}
	case '[':
		for dec.More() {
			if childErr := checkObject(dec, depth+1); childErr != nil {
				return childErr
			}
		}
	default:
		return errors.New("invalid policy JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

func oneOf(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}
