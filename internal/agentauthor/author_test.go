package agentauthor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func definition(id string) *agentdef.Definition {
	return &agentdef.Definition{
		SchemaVersion: "2", DefinitionID: id, Revision: "explicit-revision", Name: "author-test",
		Title: "Concrete title", Description: "Authored definition",
		Behavior: agentdef.Behavior{Purpose: "Concrete purpose"}, Body: "Concrete instructions",
		HarnessProfile: agentdef.HarnessProfile{Permissions: agentdef.PermissionProfile{Profile: "concrete-request"}},
		Continuity:     agentdef.Continuity{Mode: agentdef.Ephemeral},
	}
}

func pin(uri, digit string) agentdef.Ref {
	return agentdef.Ref{URI: uri, Digest: "sha256:" + strings.Repeat(digit, 64)}
}

func native(data map[string]any) agentdef.Extension {
	return agentdef.Extension{Version: agentpolicy.Version, Area: "harness_profile", Data: data}
}

func TestFlattenInheritsBehaviorWithoutRoleAuthority(t *testing.T) {
	role := definition("role-identity-never-copied")
	role.Revision = "role-revision"
	role.Behavior = agentdef.Behavior{Purpose: "Role purpose", Instructions: []agentdef.Ref{pin("role:instructions", "a")}, SOPs: []agentdef.Ref{pin("role:sop", "b")}, Completion: "Role completion"}
	role.Body = "Role body"
	role.Requirements.Tools = []string{"role-admin-tool"}
	role.HarnessProfile.Permissions.Profile = "role-admin-request"
	role.Continuity.Mode = agentdef.Durable
	role.Provenance = map[string]string{"actor": "role-actor-claim"}
	role.Extensions = map[string]agentdef.Extension{
		agentpolicy.NativeNamespace:   native(map[string]any{"class": "process", "message_wake_policy": "batch"}),
		"example.org/authority-claim": {Version: "1", Area: "harness_profile", Data: map[string]any{"grants": []any{"all"}}},
	}
	concrete := definition("concrete-explicit-id")
	concrete.Behavior = agentdef.Behavior{}
	concrete.Body = ""
	concrete.Requirements.Tools = []string{"concrete-tool-request"}
	concrete.Extensions = map[string]agentdef.Extension{agentpolicy.NativeNamespace: native(map[string]any{"message_wake_policy": "auto_summarize", "auto_recall": map[string]any{"enabled": false}})}
	wantConcrete, err := json.Marshal(concrete)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Flatten(role, concrete)
	if err != nil {
		t.Fatal(err)
	}
	if result.DefinitionID != concrete.DefinitionID || result.Revision != concrete.Revision || result.Title != concrete.Title || result.Body != role.Body || !reflect.DeepEqual(result.Behavior, role.Behavior) {
		t.Fatalf("identity or inherited behavior changed: %+v", result)
	}
	if !reflect.DeepEqual(result.Requirements, concrete.Requirements) || !reflect.DeepEqual(result.HarnessProfile, concrete.HarnessProfile) || !reflect.DeepEqual(result.Continuity, concrete.Continuity) || result.Provenance != nil || len(result.Extensions) != 1 {
		t.Fatalf("role authority or other policy leaked: %+v", result)
	}
	policy, err := agentpolicy.DecodeNative(result.Extensions[agentpolicy.NativeNamespace])
	if err != nil || policy.Class != "process" || policy.MessageWakePolicy != "auto_summarize" || policy.AutoRecall == nil || policy.AutoRecall.Enabled == nil || *policy.AutoRecall.Enabled {
		t.Fatalf("native class/explicit concrete policy precedence: %+v, %v", policy, err)
	}
	// Mutations in either direction must not alter either authored input.
	result.Behavior.Instructions[0].URI = "mutated-output"
	result.Requirements.Tools[0] = "mutated-output"
	result.Extensions[agentpolicy.NativeNamespace].Data["auto_recall"].(map[string]any)["enabled"] = true
	if role.Behavior.Instructions[0].URI != "role:instructions" {
		t.Fatal("inherited role references share storage")
	}
	gotConcrete, err := json.Marshal(concrete)
	if err != nil || !bytes.Equal(wantConcrete, gotConcrete) {
		t.Fatal("flatten or output mutation changed concrete input")
	}
}

func TestFlattenExplicitOverridesAndIsolation(t *testing.T) {
	role := definition("role")
	role.Behavior.Instructions = []agentdef.Ref{pin("role:instructions", "a")}
	role.Behavior.SOPs = []agentdef.Ref{pin("role:sop", "b")}
	role.Behavior.Hooks = []string{"role-hook"}
	role.Behavior.Completion = "Role completion"
	role.Extensions = map[string]agentdef.Extension{agentpolicy.NativeNamespace: native(map[string]any{"class": "process"})}
	concrete := definition("concrete")
	concrete.Behavior.Instructions = []agentdef.Ref{pin("concrete:instructions", "c")}
	concrete.Behavior.SOPs = []agentdef.Ref{}
	concrete.Behavior.Hooks = []string{}
	concrete.Behavior.Completion = "Concrete completion"
	concrete.Extensions = map[string]agentdef.Extension{
		agentpolicy.NativeNamespace: native(map[string]any{"class": "advisor"}),
		"example.org/optional-data": {Version: "1", Area: "behavior", Data: map[string]any{"nested": []any{map[string]any{"value": "original"}}}},
	}
	concrete.Capabilities = []agentdef.Capability{{ID: "advertised", Description: "Concrete service", Domains: []string{"local"}, Input: refPointer(pin("schema:input", "d"))}}
	concrete.HarnessProfile.Context.Policy = refPointer(pin("context:policy", "e"))
	concrete.Continuity.MemoryPolicy = refPointer(pin("memory:policy", "f"))
	result, err := Flatten(role, concrete)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, concrete) {
		t.Fatalf("explicit concrete overrides were replaced: %+v", result)
	}
	concrete.Extensions["example.org/optional-data"].Data["nested"].([]any)[0].(map[string]any)["value"] = "input changed"
	concrete.Capabilities[0].Domains[0] = "input changed"
	concrete.Capabilities[0].Input.URI = "input changed"
	concrete.HarnessProfile.Context.Policy.URI = "input changed"
	concrete.Continuity.MemoryPolicy.URI = "input changed"
	if result.Extensions["example.org/optional-data"].Data["nested"].([]any)[0].(map[string]any)["value"] != "original" || result.Capabilities[0].Domains[0] != "local" || result.Capabilities[0].Input.URI != "schema:input" || result.HarnessProfile.Context.Policy.URI != "context:policy" || result.Continuity.MemoryPolicy.URI != "memory:policy" {
		t.Fatal("flatten retained mutable concrete storage")
	}
}

func refPointer(ref agentdef.Ref) *agentdef.Ref { return &ref }

func TestFlattenRequiredExtensionRefusalAndOverride(t *testing.T) {
	reflex := agentdef.Extension{Version: "1", Area: "behavior", Mandatory: true, Data: map[string]any{"bundle": map[string]any{"uri": "reflex:role", "digest": pin("", "a").Digest}}}
	tests := []struct {
		name     string
		role     agentdef.Extension
		concrete *agentdef.Extension
		key      string
		wantErr  bool
	}{
		{"unknown mandatory", agentdef.Extension{Version: "1", Area: "behavior", Mandatory: true, Data: map[string]any{}}, nil, "example.org/unknown-behavior", true},
		{"required reflex missing", reflex, nil, agentpolicy.ReflexNamespace, true},
		{"required reflex represented", reflex, &reflex, agentpolicy.ReflexNamespace, false},
		{"required reflex explicit replacement", reflex, &agentdef.Extension{Version: "1", Area: "behavior", Data: map[string]any{"bundle": map[string]any{"uri": "reflex:concrete", "digest": pin("", "b").Digest}}}, agentpolicy.ReflexNamespace, false},
		{"required reflex unnegotiated replacement", reflex, &agentdef.Extension{Version: "2", Area: "behavior", Data: map[string]any{}}, agentpolicy.ReflexNamespace, true},
		{"required native wake missing", agentdef.Extension{Version: "1", Area: "harness_profile", Mandatory: true, Data: map[string]any{"class": "process", "message_wake_policy": "batch"}}, nil, agentpolicy.NativeNamespace, true},
		{"required native wake overridden", agentdef.Extension{Version: "1", Area: "harness_profile", Mandatory: true, Data: map[string]any{"class": "process", "message_wake_policy": "batch"}}, extensionPointer(native(map[string]any{"message_wake_policy": "auto_summarize"})), agentpolicy.NativeNamespace, false},
		{"optional unsupported role only", agentdef.Extension{Version: "2", Area: "behavior", Data: map[string]any{"hint": "adornment"}}, nil, agentpolicy.ReflexNamespace, false},
		{"invalid native policy", native(map[string]any{"class": "host-superuser"}), nil, agentpolicy.NativeNamespace, true},
		{"class into unnegotiated concrete native", native(map[string]any{"class": "process"}), &agentdef.Extension{Version: "2", Area: "harness_profile", Data: map[string]any{}}, agentpolicy.NativeNamespace, true},
		{"role host authority refused", native(map[string]any{"class": "advisor", "tool_grants": []any{"all"}}), nil, agentpolicy.NativeNamespace, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, concrete := definition("role"), definition("concrete")
			role.Extensions = map[string]agentdef.Extension{tt.key: tt.role}
			if tt.concrete != nil {
				concrete.Extensions = map[string]agentdef.Extension{tt.key: *tt.concrete}
			}
			result, err := Flatten(role, concrete)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Flatten error = %v, want refusal %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.role.Version == "2" && len(result.Extensions) != 0 {
				t.Fatal("optional unsupported role extension became output behavior")
			}
		})
	}
}

func TestFlattenRoleHooksRequireConcreteOverride(t *testing.T) {
	role, concrete := definition("role"), definition("concrete")
	role.Behavior.Hooks = []string{"required-role-hook"}
	if _, err := Flatten(role, concrete); err == nil {
		t.Fatal("unrepresented role hooks silently discarded")
	}
	for _, hooks := range [][]string{{}, {"concrete-hook"}} {
		concrete.Behavior.Hooks = hooks
		result, err := Flatten(role, concrete)
		if err != nil || !reflect.DeepEqual(result.Behavior.Hooks, hooks) {
			t.Fatalf("explicit concrete hooks override was replaced: %v", err)
		}
	}
}

func extensionPointer(extension agentdef.Extension) *agentdef.Extension { return &extension }

func TestFlattenAndRenderRefuseConflictingPins(t *testing.T) {
	role, concrete := definition("role"), definition("concrete")
	role.Behavior.Instructions = []agentdef.Ref{pin("shared:artifact", "a")}
	concrete.Requirements.Resources = []agentdef.Ref{pin("shared:artifact", "b")}
	if _, err := Flatten(role, concrete); err == nil || !strings.Contains(err.Error(), "conflicting pinned URI") {
		t.Fatalf("inherited/core pin conflict accepted: %v", err)
	}
	concrete.Behavior.Instructions = []agentdef.Ref{pin("shared:artifact", "b")}
	result, err := Flatten(role, concrete)
	if err != nil {
		t.Fatalf("explicit matching replacement refused: %v", err)
	}
	result.Extensions = map[string]agentdef.Extension{agentpolicy.ReflexNamespace: {Version: "1", Area: "behavior", Data: map[string]any{"bundle": map[string]any{"uri": "shared:artifact", "digest": pin("", "c").Digest}}}}
	if _, err := Render(result); err == nil || !strings.Contains(err.Error(), "conflicting pinned URI") {
		t.Fatalf("core/reflex pin conflict accepted: %v", err)
	}
}

func TestRenderFullDefinitionDeterministicSDKRoundtrip(t *testing.T) {
	d := definition("explicit:concrete-id")
	d.Body = "\r\nFull body\r\n\r\n---\r\nThis delimiter is body content.  \r\n"
	d.Behavior = agentdef.Behavior{Purpose: "Purpose", Instructions: []agentdef.Ref{pin("instructions:one", "a")}, SOPs: []agentdef.Ref{pin("sop:one", "b")}, Hooks: []string{"completion-hook"}, Completion: "Completion"}
	d.Capabilities = []agentdef.Capability{{ID: "service:one", Description: "Service", Domains: []string{"one", "two"}, Input: refPointer(pin("input:one", "c")), Output: refPointer(pin("output:one", "d"))}}
	d.Requirements = agentdef.Requirements{Requires: []string{"workspace"}, Uses: []string{"mail"}, Tools: []string{"tool.request"}, Skills: []agentdef.Skill{{Name: "test-skill", Content: pin("skill:one", "e")}}, Resources: []agentdef.Ref{pin("resource:one", "f")}}
	d.HarnessProfile.Steering = []agentdef.Ref{pin("steering:one", "a")}
	d.HarnessProfile.Context = agentdef.ContextPolicy{Sources: []agentdef.Ref{pin("context:source", "b")}, Policy: refPointer(pin("context:policy", "c"))}
	d.HarnessProfile.Approvals = []agentdef.Ref{pin("approval:one", "d")}
	d.HarnessProfile.Escalation = []agentdef.Ref{pin("escalation:one", "e")}
	d.Continuity = agentdef.Continuity{Mode: agentdef.Durable, MemoryPolicy: refPointer(pin("memory:one", "f")), RecoveryStrategy: refPointer(pin("recovery:one", "a"))}
	d.Presentation = agentdef.Presentation{Icon: "icon", Avatar: "avatar", Tags: []string{"one", "two"}}
	d.Provenance = map[string]string{"z-source": "z", "a-source": "a"}
	d.Extensions = map[string]agentdef.Extension{
		"example.org/optional-data": {Version: "1", Area: "requirements", Data: map[string]any{"z-last": []any{"a", true, int64(9007199254740993)}, "a-first": map[string]any{"value": "yes"}}},
		agentpolicy.NativeNamespace: native(map[string]any{"class": "advisor", "auto_recall": map[string]any{"enabled": false, "min_confidence": .7}}),
	}
	file, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := agentdef.Parse(file, agentpolicy.Option())
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := agentdef.Digest(d)
	if err != nil {
		t.Fatal(err)
	}
	gotDigest, err := agentdef.Digest(parsed)
	if err != nil || gotDigest != wantDigest {
		t.Fatalf("SDK digest changed: %s != %s, %v", gotDigest, wantDigest, err)
	}
	if parsed.DefinitionID != d.DefinitionID || parsed.Revision != d.Revision || !reflect.DeepEqual(parsed.Presentation, d.Presentation) || !reflect.DeepEqual(parsed.Provenance, d.Provenance) || parsed.Body != "Full body\n\n---\nThis delimiter is body content." {
		t.Fatalf("non-digest metadata or Markdown lost: %+v", parsed)
	}
	reRendered, err := Render(parsed)
	if err != nil || !bytes.Equal(file, reRendered) {
		t.Fatalf("canonical render is not idempotent: %v", err)
	}
	// Reverse insertion order; bytes and artifact pin must remain deterministic.
	d.Provenance = map[string]string{"a-source": "a", "z-source": "z"}
	again, err := Render(d)
	if err != nil || !bytes.Equal(file, again) || agentdef.ArtifactDigest(file) != agentdef.ArtifactDigest(again) {
		t.Fatalf("map insertion order changed artifact: %v", err)
	}
}

func TestAuthorRefusesMissingIdentityAndUnsupportedContent(t *testing.T) {
	if _, err := Flatten(nil, nil); err == nil {
		t.Fatal("missing concrete accepted")
	}
	if _, err := Render(nil); err == nil {
		t.Fatal("missing definition accepted")
	}
	for _, field := range []string{"id", "revision", "permissions", "continuity"} {
		t.Run(field, func(t *testing.T) {
			concrete := definition("concrete")
			switch field {
			case "id":
				concrete.DefinitionID = ""
			case "revision":
				concrete.Revision = ""
			case "permissions":
				concrete.HarnessProfile.Permissions.Profile = ""
			case "continuity":
				concrete.Continuity.Mode = ""
			}
			if _, err := Flatten(definition("role"), concrete); err == nil {
				t.Fatal("role supplied a missing concrete identity/policy")
			}
		})
	}
	d := definition("concrete")
	d.Extensions = map[string]agentdef.Extension{agentpolicy.NativeNamespace: native(map[string]any{"model": "host-model"})}
	if _, err := Render(d); err == nil {
		t.Fatal("host model field accepted as native intrinsic behavior")
	}
	d.Extensions = map[string]agentdef.Extension{"example.org/optional-data": {Version: "1", Area: "behavior", Data: map[string]any{"value": make(chan int)}}}
	if _, err := Flatten(nil, d); err == nil {
		t.Fatal("non-JSON content accepted")
	}
}
