// Package agentauthor materializes authored role behavior into flat agentdef
// files. It has no runtime role lookup, host settings, enrollment or grant API.
package agentauthor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// Flatten keeps concrete identity and policy. Empty behavioral scalars inherit
// from role; nil instruction/SOP lists inherit, while nonnil lists (even empty
// ones) override. Hooks stay concrete; role hooks require an explicit concrete
// override. Only native class is inherited from role extensions. A nil role
// authors a standalone concrete definition. Inputs are never mutated or shared.
func Flatten(role, concrete *agentdef.Definition) (*agentdef.Definition, error) {
	if concrete == nil {
		return nil, errors.New("concrete definition is required")
	}
	if role != nil {
		if err := role.Validate(agentpolicy.Option()); err != nil {
			return nil, fmt.Errorf("role: %w", err)
		}
	}
	// Validate JSON-compatible extension data before recursively copying it.
	if _, err := json.Marshal(concrete); err != nil {
		return nil, fmt.Errorf("concrete: %w", err)
	}
	copyValue, err := copyOwned(reflect.ValueOf(concrete))
	if err != nil {
		return nil, err
	}
	out := copyValue.Interface().(*agentdef.Definition)
	if role != nil {
		if out.Behavior.Purpose == "" {
			out.Behavior.Purpose = role.Behavior.Purpose
		}
		if out.Body == "" {
			out.Body = role.Body
		}
		if out.Behavior.Completion == "" {
			out.Behavior.Completion = role.Behavior.Completion
		}
		if out.Behavior.Instructions == nil && role.Behavior.Instructions != nil {
			out.Behavior.Instructions = append([]agentdef.Ref{}, role.Behavior.Instructions...)
		}
		if out.Behavior.SOPs == nil && role.Behavior.SOPs != nil {
			out.Behavior.SOPs = append([]agentdef.Ref{}, role.Behavior.SOPs...)
		}
		if len(role.Behavior.Hooks) > 0 && out.Behavior.Hooks == nil {
			return nil, errors.New("role hooks require an explicit concrete override")
		}
		if err := flattenExtensions(role, out); err != nil {
			return nil, err
		}
	}
	if err := out.Validate(agentpolicy.Option()); err != nil {
		return nil, err
	}
	if err := checkPins(out); err != nil {
		return nil, err
	}
	return out, nil
}

func flattenExtensions(role, out *agentdef.Definition) error {
	keys := make([]string, 0, len(role.Extensions))
	for namespace := range role.Extensions {
		keys = append(keys, namespace)
	}
	sort.Strings(keys)
	for _, namespace := range keys {
		inherited := role.Extensions[namespace]
		concrete, exists := out.Extensions[namespace]
		if namespace == agentpolicy.NativeNamespace {
			if inherited.Version != agentpolicy.Version {
				continue // optional unnegotiated adornment remains only in role
			}
			// DecodeNative is also applied by the authoritative validator. Keeping
			// raw concrete data avoids introducing defaulted or host-owned values.
			policy, err := agentpolicy.DecodeNative(inherited)
			if err != nil {
				return err
			}
			if inherited.Mandatory {
				if exists {
					if _, err := agentpolicy.DecodeNative(concrete); err != nil {
						return fmt.Errorf("required role native override: %w", err)
					}
				}
				for key := range inherited.Data {
					if key == "class" {
						continue
					}
					if _, represented := concrete.Data[key]; !represented {
						return fmt.Errorf("required role native behavior %q needs an explicit concrete override", key)
					}
				}
			}
			_, hasClass := inherited.Data["class"]
			if _, overridden := concrete.Data["class"]; hasClass && !overridden {
				if exists {
					if _, err := agentpolicy.DecodeNative(concrete); err != nil {
						return fmt.Errorf("native class inheritance: %w", err)
					}
				}
				if !exists {
					concrete = agentdef.Extension{Version: inherited.Version, Area: inherited.Area, Mandatory: inherited.Mandatory, Data: map[string]any{}}
				}
				if concrete.Data == nil {
					return errors.New("concrete native-policy data must be an object")
				}
				concrete.Data["class"] = policy.Class
				if out.Extensions == nil {
					out.Extensions = make(map[string]agentdef.Extension)
				}
				out.Extensions[namespace] = concrete
			} else if inherited.Mandatory && !exists {
				return errors.New("required role native policy is not represented")
			}
			continue
		}
		// Only class has extension inheritance semantics. Known mandatory role
		// behavior requires an explicitly negotiated concrete replacement; unknown
		// mandatory extensions have already been refused by role.Validate.
		if inherited.Mandatory && !exists {
			return fmt.Errorf("required role extension %q needs an explicit concrete override", namespace)
		}
		if inherited.Mandatory && namespace == agentpolicy.ReflexNamespace {
			if _, err := agentpolicy.DecodeReflex(concrete); err != nil {
				return fmt.Errorf("required role reflex override: %w", err)
			}
		}
	}
	return nil
}

// Render writes deterministic full v2 frontmatter and normalized Markdown. JSON
// is a YAML subset and avoids map ordering and implicit YAML scalar conversions.
// The SDK parses the result and must recover the same full definition and digest.
func Render(d *agentdef.Definition) ([]byte, error) {
	if err := d.Validate(agentpolicy.Option()); err != nil {
		return nil, err
	}
	if err := checkPins(d); err != nil {
		return nil, err
	}
	full, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(full, &fields); err != nil {
		return nil, err
	}
	delete(fields, "body")
	frontmatter, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(d.Body, "\r\n", "\n"), "\r", "\n"))
	file := append([]byte("---\n"), frontmatter...)
	file = append(file, []byte("\n---\n\n"+body+"\n")...)
	parsed, err := agentdef.Parse(file, agentpolicy.Option())
	if err != nil {
		return nil, fmt.Errorf("render roundtrip: %w", err)
	}
	wantDigest, err := agentdef.Digest(d)
	if err != nil {
		return nil, err
	}
	gotDigest, err := agentdef.Digest(parsed)
	if err != nil {
		return nil, err
	}
	// Compare full JSON as well, so identity, presentation and provenance cannot
	// disappear even though the SDK deliberately excludes them from its digest.
	fields["body"], err = json.Marshal(body)
	if err != nil {
		return nil, err
	}
	want, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	got, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	var gotFields map[string]json.RawMessage
	if err = json.Unmarshal(got, &gotFields); err != nil {
		return nil, err
	}
	got, err = json.Marshal(gotFields)
	if err != nil {
		return nil, err
	}
	if wantDigest != gotDigest || !bytes.Equal(want, got) {
		return nil, errors.New("render cannot represent definition without changing its content")
	}
	return file, nil
}

// checkPins walks all core pins and the negotiated reflex pin without resolving
// any resource. A URI cannot request two different artifacts in one definition.
func checkPins(d *agentdef.Definition) error {
	seen := map[string]string{}
	check := func(ref agentdef.Ref) error {
		if digest, exists := seen[ref.URI]; exists && digest != ref.Digest {
			return fmt.Errorf("conflicting pinned URI %q", ref.URI)
		}
		seen[ref.URI] = ref.Digest
		return nil
	}
	var walk func(reflect.Value) error
	walk = func(v reflect.Value) error {
		if v.Type() == reflect.TypeFor[agentdef.Ref]() {
			return check(v.Interface().(agentdef.Ref))
		}
		switch v.Kind() {
		case reflect.Pointer:
			if !v.IsNil() {
				return walk(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if err := walk(v.Field(i)); err != nil {
					return err
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				if err := walk(v.Index(i)); err != nil {
					return err
				}
			}
		default:
			// Other core fields do not contain typed resource pins.
		}
		return nil
	}
	if err := walk(reflect.ValueOf(d)); err != nil {
		return err
	}
	if extension, exists := d.Extensions[agentpolicy.ReflexNamespace]; exists {
		policy, err := agentpolicy.DecodeReflex(extension)
		if err != nil {
			return err
		}
		return check(policy.Bundle)
	}
	return nil
}

// copyOwned preserves explicit empty lists and numeric types while severing
// every input pointer, slice and map. JSON validation above refuses cycles.
func copyOwned(v reflect.Value) (reflect.Value, error) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		child, err := copyOwned(v.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(v.Type()).Elem()
		if v.Kind() == reflect.Pointer {
			out = reflect.New(v.Type().Elem())
			out.Elem().Set(child)
		} else {
			out.Set(child)
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		out := reflect.New(v.Type()).Elem()
		if v.Kind() == reflect.Slice {
			out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		}
		for i := 0; i < v.Len(); i++ {
			child, err := copyOwned(v.Index(i))
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(child)
		}
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type()), nil
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			if iter.Key().Kind() != reflect.String {
				return reflect.Value{}, errors.New("authored map keys must be strings")
			}
			child, err := copyOwned(iter.Value())
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(iter.Key(), child)
		}
		return out, nil
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			if !out.Field(i).CanSet() {
				return reflect.Value{}, errors.New("authored data cannot contain unexported fields")
			}
			child, err := copyOwned(v.Field(i))
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(child)
		}
		return out, nil
	default:
		return v, nil
	}
}
