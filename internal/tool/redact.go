package tool

import (
	"encoding/json"
	"regexp"
	"strings"
)

// RedactedPlaceholder replaces a secret value in persisted tool arguments.
const RedactedPlaceholder = "[REDACTED]"

// ArgumentRedactor rewrites tool-call arguments before they are persisted. It
// is the seam for a future dev-mode policy or a shared redaction library
// (go-secretref): swap the redactor on ResultCacheConfig, nothing else changes.
// The count is how many values were replaced.
type ArgumentRedactor interface {
	Redact(toolName string, args map[string]any) (redacted map[string]any, count int)
}

// NamePatternRedactor redacts the value of any argument whose key names a
// secret, at any nesting depth, whatever the value's type. It is a name-pattern
// policy: it cannot recognize a secret under an innocuous key (a "value" field
// paired with a "name": "API_TOKEN" sibling, or a token inside file contents).
// Over-redaction is the deliberate failure direction.
type NamePatternRedactor struct{}

// secretKeyFragments are matched as substrings of the key after it is
// lowercased and stripped of everything but letters and digits, so api_key,
// apiKey, API-KEY and "api key" are one spelling. Short fragments that sit
// inside ordinary words ("auth" in author, "pin" in mapping, "otp" in
// footprint) are deliberately absent.
var secretKeyFragments = []string{
	"password", "passwd", "passphrase", "pwd",
	"secret", "token", "apikey", "accesskey", "privatekey", "signingkey", "encryptionkey",
	"authorization", "authkey", "authheader", "credential", "cookie", "bearer", "jwt",
}

// benignKeys contain a secret fragment but are not secrets. Kept tiny: a key
// missing here is merely over-redacted.
var benignKeys = map[string]bool{
	"maxtokens": true, "maxoutputtokens": true, "maxinputtokens": true,
	"numtokens": true, "totaltokens": true, "inputtokens": true, "outputtokens": true,
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// IsSecretKey reports whether an argument key names a secret.
func IsSecretKey(key string) bool {
	k := nonAlnum.ReplaceAllString(strings.ToLower(key), "")
	if k == "" || benignKeys[k] {
		return false
	}
	for _, frag := range secretKeyFragments {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

func (NamePatternRedactor) Redact(_ string, args map[string]any) (map[string]any, int) {
	count := 0
	out, _ := redactValue(args, &count).(map[string]any)
	return out, count
}

// reSecretAssign matches NAME=value / NAME: value where NAME names a secret,
// as found in shell commands and env-style text. reBearer matches an
// Authorization credential or a bare bearer token.
var (
	reSecretAssign = regexp.MustCompile(`(?i)([A-Za-z0-9_.\-]*(?:password|passwd|passphrase|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credential)[A-Za-z0-9_.\-]*["']?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s"',;&]+)`)
	reBearer       = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{8,}`)
)

func redactString(s string, count *int) string {
	if s == "" {
		return s
	}
	// A string that is itself a JSON document (tool args nested as text) is
	// redacted structurally, then re-encoded.
	if t := strings.TrimSpace(s); len(t) > 1 && (t[0] == '{' || t[0] == '[') {
		var inner any
		if json.Unmarshal([]byte(t), &inner) == nil {
			n := 0
			red := redactValue(inner, &n)
			if n > 0 {
				*count += n
				if enc, err := json.Marshal(red); err == nil {
					return string(enc)
				}
			}
		}
	}
	out := reSecretAssign.ReplaceAllStringFunc(s, func(m string) string {
		*count++
		sub := reSecretAssign.FindStringSubmatch(m)
		return sub[1] + RedactedPlaceholder
	})
	out = reBearer.ReplaceAllStringFunc(out, func(m string) string {
		*count++
		return strings.Fields(m)[0] + " " + RedactedPlaceholder
	})
	return out
}

// redactValue returns a copy; the caller's map is never mutated.
func redactValue(v any, count *int) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if IsSecretKey(k) && val != nil {
				out[k] = RedactedPlaceholder
				*count++
				continue
			}
			out[k] = redactValue(val, count)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactValue(val, count)
		}
		return out
	case string:
		return redactString(t, count)
	default:
		return v
	}
}

// normalizeArgs round-trips the arguments through JSON so typed values
// (structs, json.RawMessage, []string) become the generic shapes the redactor
// walks. If that fails the caller persists nothing (fail closed).
func normalizeArgs(args map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
