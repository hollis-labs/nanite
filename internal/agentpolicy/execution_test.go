package agentpolicy

import (
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
)

func baseDefinition() *agentdef.Definition {
	return &agentdef.Definition{
		SchemaVersion: "2", DefinitionID: "def:x", Revision: "1", Name: "x", Description: "x",
		Behavior:       agentdef.Behavior{Purpose: "Answer."},
		HarnessProfile: agentdef.HarnessProfile{Permissions: agentdef.PermissionProfile{Profile: "default"}},
		Continuity:     agentdef.Continuity{Mode: agentdef.Ephemeral},
		Body:           "Body.",
	}
}

func ref(s string) agentdef.Ref {
	return agentdef.Ref{URI: "file:" + s, Digest: agentdef.ArtifactDigest([]byte(s))}
}

func TestValidateExecutionAcceptsWhatTheTableApplies(t *testing.T) {
	d := baseDefinition()
	d.Capabilities = []agentdef.Capability{{ID: "review.code", Description: "Reviews."}}
	d.Requirements.Uses = []string{"optional-cap"}
	d.Requirements.Tools = []string{"dev_read"}
	d.Requirements.Skills = []agentdef.Skill{{Name: "review", Content: ref("tree")}}
	d.Requirements.Resources = []agentdef.Ref{ref("res")}
	d.HarnessProfile.Steering = []agentdef.Ref{ref("steer")}
	d.HarnessProfile.Context.Sources = []agentdef.Ref{ref("ctx")}
	policy := ref("policy")
	d.HarnessProfile.Context.Policy = &policy
	d.Continuity.MemoryPolicy = &policy
	d.Continuity.RecoveryStrategy = &policy
	if err := ValidateExecution(d); err != nil {
		t.Fatalf("ValidateExecution = %v", err)
	}
	for _, f := range ExecutionReport(d) {
		if f.Disposition == Refused {
			t.Errorf("%s refused: %s", f.Field, f.Detail)
		}
	}
}

func TestValidateExecutionRefusesAndNamesTheField(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*agentdef.Definition)
		want   string
	}{
		"hook":       {func(d *agentdef.Definition) { d.Behavior.Hooks = []string{"guard"} }, `"guard"`},
		"requires":   {func(d *agentdef.Definition) { d.Requirements.Requires = []string{"needed-cap"} }, `"needed-cap"`},
		"approvals":  {func(d *agentdef.Definition) { d.HarnessProfile.Approvals = []agentdef.Ref{ref("a")} }, "harness_profile.approvals"},
		"escalation": {func(d *agentdef.Definition) { d.HarnessProfile.Escalation = []agentdef.Ref{ref("e")} }, "harness_profile.escalation"},
		"durable":    {func(d *agentdef.Definition) { d.Continuity.Mode = agentdef.Durable }, "continuity.mode"},
		"profile":    {func(d *agentdef.Definition) { d.HarnessProfile.Permissions.Profile = "strict" }, "permissions.profile"},
	} {
		t.Run(name, func(t *testing.T) {
			d := baseDefinition()
			tc.mutate(d)
			err := ValidateExecution(d)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %s", err, tc.want)
			}
		})
	}
}

func TestRenderOrdersSteeringAfterInstructionsAndPlantsFiles(t *testing.T) {
	d := baseDefinition()
	d.Behavior.Completion = "All done."
	d.Behavior.Instructions = []agentdef.Ref{ref("instr")}
	d.HarnessProfile.Steering = []agentdef.Ref{ref("steer-1"), ref("steer-2")}
	d.HarnessProfile.Context.Sources = []agentdef.Ref{ref("ctx")}
	d.Requirements.Resources = []agentdef.Ref{ref("res")}
	read := func(r agentdef.Ref) ([]byte, error) { return []byte(strings.TrimPrefix(r.URI, "file:")), nil }
	out, err := Render(d, read)
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for _, part := range []string{"Body.", "instr", "Completion: All done.", "steer-1", "steer-2", "- agent/ctx"} {
		at := strings.Index(out.Instructions, part)
		if at < last {
			t.Fatalf("%q is out of order in:\n%s", part, out.Instructions)
		}
		last = at
	}
	if strings.Contains(out.Instructions, "agent/res") {
		t.Error("a resource that is not a context source must not be listed")
	}
	if string(out.Files["agent/ctx"]) != "ctx" || string(out.Files["agent/res"]) != "res" || len(out.Files) != 2 {
		t.Errorf("files = %v", out.Files)
	}
}

func TestRenderVerifiesPinsAndConfinesPaths(t *testing.T) {
	d := baseDefinition()
	d.Requirements.Resources = []agentdef.Ref{{URI: "file:../../etc/passwd", Digest: agentdef.ArtifactDigest([]byte("x"))}}
	out, err := Render(d, func(agentdef.Ref) ([]byte, error) { return []byte("x"), nil })
	if err != nil {
		t.Fatal(err)
	}
	for p := range out.Files {
		if !strings.HasPrefix(p, FilesRoot+"/") || strings.Contains(p, "..") {
			t.Errorf("planted path %q escapes the files root", p)
		}
	}
	if _, err = Render(d, func(agentdef.Ref) ([]byte, error) { return []byte("tampered"), nil }); err == nil {
		t.Fatal("rendered a document that does not match its pin")
	}
}

func TestSkillTreeRoundTripAndRefusals(t *testing.T) {
	files := map[string][]byte{"SKILL.md": []byte("# s\n"), "scripts/run.sh": []byte("echo\n")}
	a, err := EncodeSkillTree(files)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := EncodeSkillTree(files)
	if string(a) != string(b) {
		t.Fatal("encoding is not deterministic")
	}
	back, err := DecodeSkillTree(a)
	if err != nil || string(back["scripts/run.sh"]) != "echo\n" || len(back) != 2 {
		t.Fatalf("round trip = %v, %v", back, err)
	}
	if _, err = EncodeSkillTree(map[string][]byte{"other.md": nil}); err == nil {
		t.Error("encoded a tree with no SKILL.md")
	}
	if _, err = EncodeSkillTree(map[string][]byte{"SKILL.md": nil, "../x": nil}); err == nil {
		t.Error("encoded a path outside the folder")
	}
	if _, err = DecodeSkillTree([]byte(`{"format":"nanite-skill-tree/v1","files":[{"path":"/abs","content":""},{"path":"SKILL.md","content":""}]}`)); err == nil {
		t.Error("decoded an absolute path")
	}
}
