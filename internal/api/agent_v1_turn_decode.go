package api

import (
	"bytes"
	"encoding/json"
	"errors"
)

// UnmarshalJSON keeps the turn envelope closed and exact, including its
// client_context key. A duplicate descriptor must not silently replace one.
func (r *agentV1TurnRequest) UnmarshalJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("turn object required")
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("duplicate or invalid turn field")
		}
		switch key {
		case "content", "delivery", "effort", "delta_mode", "client_context":
		default:
			return errors.New("unknown turn field")
		}
		seen[key] = true
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return errors.New("invalid turn value")
		}
	}
	if _, err = d.Token(); err != nil {
		return errors.New("invalid turn object")
	}
	type plain agentV1TurnRequest
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode((*plain)(r)); err != nil {
		return errors.New("invalid turn field type")
	}
	return nil
}
