package registryagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/hollis-labs/nanite/internal/agentlock"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/toolregistry"
)

func fixture(t *testing.T) (Fake, Reference) {
	t.Helper()
	ref := Reference{Authority: "registry", Kind: "agent", ID: "fixture"}
	profile := Reference{Authority: "registry", Kind: "launch_profile", ID: "profile"}
	skill := Reference{Authority: "registry", Kind: "skill", ID: "skill"}
	doc := Reference{Authority: "registry", Kind: "document", ID: "context"}
	variant := Reference{Authority: "registry", Kind: "document", ID: "variant"}
	makeRecord := func(ref Reference, payload map[string]any) Record {
		payload["schema"] = ref.Kind + "/1"
		payload["id"] = ref.ID
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return Record{ref, "r1", raw}
	}
	return Fake{
		makeRecord(ref, map[string]any{"name": "fixture", "summary": "Fixture purpose.", "charter": "Help with the fixture.", "roles": []string{"assistant"}, "launch_profile": versioned{profile, "r1"}, "skills": []Reference{skill}, "context": []Reference{doc}, "variants": []Reference{variant}}),
		makeRecord(profile, map[string]any{"name": "profile", "source": versioned{Reference{Authority: "tether", Kind: "launch_profile", ID: "source"}, "s1"}, "launcher": "nanite", "host": "surface-module:fixture/chat", "runtime": map[string]any{"provider": "openai", "model": "test", "settings": map[string]any{"max_steps": 4, "daily_token_budget": 5000, "cache_ttl": ""}}}),
		makeRecord(skill, map[string]any{"name": "lookup", "summary": "Read fixture context.", "instructions": "Read the local context before answering."}),
		makeRecord(doc, map[string]any{"name": "area", "subtype": "document", "format": "markdown", "body": "Fixture context."}),
		makeRecord(variant, map[string]any{"name": "auditor", "subtype": "document", "format": "markdown", "body": "---\nread_only: true\n---\nReview the fixture."}),
	}, ref
}

func TestMaterializeRoundTripAndStaleness(t *testing.T) {
	records, ref := fixture(t)
	dir := filepath.Join(t.TempDir(), "agent")
	first, err := Materialize(t.Context(), records, ref, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || len(first.Manifest.Sources) != 5 {
		t.Fatalf("first: %+v", first)
	}
	snap, err := agentdir.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Profile.Runtime.DailyTokenBudget != 5000 || snap.Profile.Kind != "embedded" {
		t.Fatalf("profile: %+v", snap.Profile)
	}
	second, err := Materialize(t.Context(), records, ref, dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || !second.Manifest.MaterializedAt.Equal(first.Manifest.MaterializedAt) {
		t.Fatal("unchanged source was rematerialized")
	}
	records[0].Revision = "r2"
	third, err := Materialize(t.Context(), records, ref, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Changed || third.Backup == "" {
		t.Fatal("source revision change not materialized")
	}
	if _, err := os.Stat(filepath.Join(third.Backup, "agent.md")); err != nil {
		t.Fatal("old folder not retained", err)
	}
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.Record(t.Context(), "remember this", "stored answer"); err != nil {
		t.Fatal(err)
	}
	if err := conversation.SetHostState(t.Context(), "fixture", "retained"); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		same, err := Materialize(t.Context(), records, ref, dir, Options{Force: force})
		if err != nil || same.Changed {
			t.Fatal("unchanged running agent rejected or replaced", err)
		}
	}
	records[0].Revision = "r3"
	if _, err := Materialize(t.Context(), records, ref, dir, Options{Force: true}); err == nil {
		t.Fatal("open runtime replaced")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	updated, err := Materialize(t.Context(), records, ref, dir)
	if err != nil || !updated.Changed {
		t.Fatal("closed runtime update failed", err)
	}
	reopened, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	restored, err := reopened.Conversation(t.Context(), conversation.ID())
	if err != nil {
		t.Fatal("conversation lost", err)
	}
	history, err := restored.History(t.Context())
	if err != nil || len(history) != 2 {
		t.Fatal("history lost", history, err)
	}
	value, err := restored.HostState(t.Context(), "fixture")
	if err != nil || value != "retained" {
		t.Fatal("host state lost", value, err)
	}
}

func TestMaterializeFailureLeavesDestination(t *testing.T) {
	records, ref := fixture(t)
	dir := filepath.Join(t.TempDir(), "agent")
	if _, err := Materialize(t.Context(), records, ref, dir); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	before, err := root.ReadFile("agent.md")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(Fake) Fake{
		"missing reference": func(f Fake) Fake { return f[:2] },
		"wrong revision":    func(f Fake) Fake { f[1].Revision = "r2"; return f },
		"oversized":         func(f Fake) Fake { f[0].Payload = []byte(strings.Repeat("x", maxRecordBytes+1)); return f },
		"unsafe name": func(f Fake) Fake {
			f[2].Payload = []byte(strings.Replace(string(f[2].Payload), `"lookup"`, `"../escape"`, 1))
			return f
		},
		"secret": func(f Fake) Fake {
			var p map[string]any
			_ = json.Unmarshal(f[0].Payload, &p)
			fixtureValue := "not-a-real-value"
			p["metadata"] = map[string]string{"password": fixtureValue}
			f[0].Payload, _ = json.Marshal(p)
			return f
		},
		"unknown runtime": func(f Fake) Fake {
			var p map[string]any
			_ = json.Unmarshal(f[1].Payload, &p)
			p["runtime"].(map[string]any)["env"] = map[string]string{"X": "value"}
			f[1].Payload, _ = json.Marshal(p)
			return f
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := append(Fake(nil), records...)
			if _, err := Materialize(t.Context(), edit(f), ref, dir); err == nil {
				t.Fatal("invalid input accepted")
			}
			current, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = current.Close() }()
			after, err := current.ReadFile("agent.md")
			if err != nil || string(after) != string(before) {
				t.Fatal("destination changed", err)
			}
		})
	}
}

func TestMaterializeOwnershipAndForce(t *testing.T) {
	records, ref := fixture(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(t.Context(), records, ref, dir); err == nil {
		t.Fatal("unmanaged folder replaced")
	}
	result, err := Materialize(t.Context(), records, ref, dir, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(result.Backup, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatal("force destroyed prior content", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(t.Context(), records, ref, link, Options{Force: true}); err == nil {
		t.Fatal("symlink target accepted")
	}
}

func TestRejectDuplicateJSON(t *testing.T) {
	if _, err := validate(t.Context(), []byte(`{"schema":"agent/1","schema":"document/1"}`)); err == nil {
		t.Fatal("duplicate accepted")
	}
}

func TestConcurrentMaterializersPublishOnce(t *testing.T) {
	records, ref := fixture(t)
	dir := filepath.Join(t.TempDir(), "agent")
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 2)
	for range 2 {
		go func() { r, e := Materialize(t.Context(), records, ref, dir); done <- outcome{r, e} }()
	}
	changed := 0
	for range 2 {
		o := <-done
		if o.err != nil {
			t.Fatal(o.err)
		}
		if o.result.Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("published %d times", changed)
	}
}

type resolverFunc func(context.Context, Reference, string) (Record, error)

func (f resolverFunc) Get(ctx context.Context, ref Reference, revision string) (Record, error) {
	return f(ctx, ref, revision)
}

func TestCancellationAndDestinationRace(t *testing.T) {
	for _, mode := range []string{"cancel", "appeared"} {
		t.Run(mode, func(t *testing.T) {
			records, ref := fixture(t)
			dir := filepath.Join(t.TempDir(), "agent")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			resolver := resolverFunc(func(ctx context.Context, requested Reference, revision string) (Record, error) {
				if requested.Kind == "agent" {
					if mode == "cancel" {
						cancel()
					} else if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				return records.Get(ctx, requested, revision)
			})
			if _, err := Materialize(ctx, resolver, ref, dir); err == nil {
				t.Fatal("publication succeeded after cancellation or destination creation")
			}
			if mode == "cancel" {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("cancelled materialization published", err)
				}
			} else {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatal("new destination modified", err)
				}
			}
		})
	}
}

type replacedPackage struct {
	toolregistry.Registry
	record toolregistry.Record
}

func (r replacedPackage) Resolve(context.Context, string) (toolregistry.Record, error) {
	return r.record, nil
}

func TestToolPackagePins(t *testing.T) {
	records, ref := fixture(t)
	source := t.TempDir()
	root, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll("tools/echo", 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"tools/echo/tool.yaml": "name: echo\ndescription: Echo input\nversion: 1.0.0\nkind: command\nentry: run.sh\n",
		"tools/echo/run.sh":    "#!/bin/sh\ncat\n",
	} {
		if err := root.WriteFile(name, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	client := toolregistry.LocalClient(t.TempDir())
	pkg, err := toolregistry.Publish(t.Context(), client, source, "echo", "msg://agent/agent-mux/agt_fixture")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(records[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["tools"] = []Reference{{Authority: "registry", Kind: "tool-package", ID: pkg.ID}, {Authority: "registry", Kind: "tool", ID: "ordinary"}}
	records[0].Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "agent")
	first, err := Materialize(t.Context(), records, ref, dir, Options{Tools: &client})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Manifest.Tools) != 2 || len(first.Manifest.Sources) != 6 {
		t.Fatal("missing tool provenance")
	}
	installed, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = installed.Close() }()
	pins, err := installed.ReadFile("tools.lock.json")
	if err != nil || !strings.Contains(string(pins), pkg.Digest) {
		t.Fatal("missing digest pin", err)
	}
	if _, err := installed.Stat("tools/echo/run.sh"); err != nil {
		t.Fatal(err)
	}
	second, err := Materialize(t.Context(), records, ref, dir, Options{Tools: &client})
	if err != nil || second.Changed {
		t.Fatal("same pins changed", err)
	}
	changed := pkg
	changed.Digest = "sha256:" + strings.Repeat("0", 64)
	bad := toolregistry.Client{Records: replacedPackage{client.Records, changed}, Blobs: client.Blobs}
	if _, err := Materialize(t.Context(), records, ref, dir, Options{Tools: &bad}); err == nil {
		t.Fatal("changed package pin accepted")
	}
	current, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = current.Close() }()
	after, err := current.ReadFile("tools.lock.json")
	if err != nil || string(after) != string(pins) {
		t.Fatal("failure changed destination pins", err)
	}
}

func TestRuntimeOpeningDuringResolutionPreventsSwap(t *testing.T) {
	records, ref := fixture(t)
	dir := filepath.Join(t.TempDir(), "agent")
	if _, err := Materialize(t.Context(), records, ref, dir); err != nil {
		t.Fatal(err)
	}
	records[0].Revision = "r2"
	var running *agent.Agent
	resolver := resolverFunc(func(ctx context.Context, requested Reference, revision string) (Record, error) {
		if requested.Kind == "agent" {
			var err error
			running, err = agent.Open(ctx, agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = running.Close() })
		}
		return records.Get(ctx, requested, revision)
	})
	if _, err := Materialize(t.Context(), resolver, ref, dir); err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatal("runtime opened during build was replaced", err)
	}
	snap, err := agentdir.Load(dir)
	if err != nil || snap.Definition.Revision != "r1" {
		t.Fatal("destination changed", err)
	}
	if err := running.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(t.Context(), records, ref, dir); err != nil {
		t.Fatal(err)
	}
}

func TestRejectAuthoredEdgeCases(t *testing.T) {
	for _, mode := range []string{"agent name", "child name", "duplicate", "document kind", "variant", "secret", "identity", "total size", "authority"} {
		t.Run(mode, func(t *testing.T) {
			records, ref := fixture(t)
			edit := func(index int, fn func(map[string]any)) {
				var value map[string]any
				if err := json.Unmarshal(records[index].Payload, &value); err != nil {
					t.Fatal(err)
				}
				fn(value)
				var err error
				records[index].Payload, err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "agent name":
				edit(0, func(v map[string]any) { v["name"] = "Upper" })
			case "child name":
				edit(2, func(v map[string]any) { v["name"] = "Upper" })
			case "duplicate":
				edit(0, func(v map[string]any) { v["context"] = []Reference{records[3].Reference, records[3].Reference} })
			case "document kind":
				edit(0, func(v map[string]any) { v["context"] = []Reference{records[2].Reference} })
			case "variant":
				edit(4, func(v map[string]any) { v["body"] = "---\nread_only: not-a-bool\n---\nInvalid variant." })
			case "secret":
				edit(0, func(v map[string]any) { v["charter"] = "ghp_" + strings.Repeat("aB9x", 10) })
			case "identity":
				records[0].Reference.ID = "different"
			case "authority":
				edit(0, func(v map[string]any) {
					v["tools"] = []Reference{{Authority: "other", Kind: "tool-package", ID: "tool-package:agt_fixture/echo@1.0.0"}}
				})
			case "total size":
				refs := []Reference{}
				for i := range 10 {
					child := records[3]
					child.Reference.ID = fmt.Sprintf("context-%d", i)
					payload := map[string]any{"schema": "document/1", "id": child.Reference.ID, "name": child.Reference.ID, "subtype": "document", "format": "markdown", "body": strings.Repeat("x", 900000)}
					child.Payload, _ = json.Marshal(payload)
					records = append(records, child)
					refs = append(refs, child.Reference)
				}
				edit(0, func(v map[string]any) { v["context"] = refs })
			}
			resolver := Resolver(records)
			if mode == "identity" {
				resolver = resolverFunc(func(context.Context, Reference, string) (Record, error) { return records[0], nil })
			}
			if _, err := Materialize(t.Context(), resolver, ref, filepath.Join(t.TempDir(), "agent")); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestPublicationLockAndStageCleanup(t *testing.T) {
	records, ref := fixture(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "agent")
	abandoned := filepath.Join(parent, ".nanite-stage-agent-"+rand.Text())
	backup := filepath.Join(parent, ".nanite-backup-retained")
	for _, path := range []string{abandoned, backup} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Materialize(t.Context(), records, ref, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("abandoned stage remains", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal("backup removed", err)
	}
	guard, err := agentlock.Publication(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Close() }()
	if opened, err := agent.Open(t.Context(), agent.Options{Dir: dir, Provider: fake.New(), GlobalSkillsDir: "-"}); err == nil {
		_ = opened.Close()
		t.Fatal("runtime opened during publication")
	}
}
