package registryagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/hollis-labs/nanite/internal/agentlock"
	"maps"
	"net/http"
	"net/http/httptest"
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
		return Record{Reference: ref, Revision: "r1", Payload: raw}
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
	records, _ := fixture(t)
	valid := records[0].Payload
	if _, err := validate(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	// Under last-wins decoding this payload is the valid one above, so only
	// the duplicate-key check can refuse it.
	duplicate := append([]byte(`{"id":"decoy",`), valid[1:]...)
	if _, err := validate(t.Context(), duplicate); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate key not refused by its own check: %v", err)
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
	t.Run("carry forward pins on update", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "agent")
		if _, err := Materialize(t.Context(), records, ref, target, Options{Tools: &client}); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(filepath.Join(target, "tools.lock.json")) // #nosec G304 -- target is created under t.TempDir.
		if err != nil {
			t.Fatal(err)
		}
		updated := append(Fake(nil), records...)
		var body map[string]any
		if err := json.Unmarshal(updated[0].Payload, &body); err != nil {
			t.Fatal(err)
		}
		// Removed tool refs must not erase the previously established pins.
		// No install occurs on this update, so it cannot recreate a missing lock.
		delete(body, "tools")
		updated[0].Payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		updated[0].Revision = "r2"
		result, err := Materialize(t.Context(), updated, ref, target)
		if err != nil || !result.Changed {
			t.Fatal("update did not publish", err)
		}
		after, err := os.ReadFile(filepath.Join(target, "tools.lock.json")) // #nosec G304 -- target is created under t.TempDir.
		if err != nil || string(after) != string(before) {
			t.Fatal("update lost prior tool pins", err)
		}
	})
	// A provenance-only mutation still verifies against the same archive. It
	// must be refused by the lock carried into the staging directory.
	changedProvenance := pkg
	changedProvenance.Provenance.SourceRef = "different-source"
	mutated := toolregistry.Client{Records: replacedPackage{client.Records, changedProvenance}, Blobs: client.Blobs}
	if _, err := Materialize(t.Context(), records, ref, dir, Options{Tools: &mutated}); err == nil {
		t.Fatal("carried-forward pin did not protect provenance")
	}
	t.Run("live package contract", func(t *testing.T) {
		payload := map[string]any{"schema": "tool-package/1", "id": strings.TrimPrefix(pkg.ID, "tool-package:"), "publisher": "agt_fixture", "name": pkg.Name, "version": pkg.Version, "digest": pkg.Digest, "artifact": pkg.Artifact, "entry": pkg.Entry, "execution": map[string]string{"kind": pkg.Execution}, "needs": []any{}, "env": []string{}, "provenance": pkg.Provenance}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("id") != pkg.ID {
				t.Error("wrong package identity")
			}
			_ = json.NewEncoder(w).Encode(liveRecord{ID: pkg.ID, Kind: "tool-package", Schema: "tool-package/1", Revision: "package-r1", Publication: "draft", Payload: raw})
		}))
		defer server.Close()
		live, err := NewLive(LiveConfig{URL: server.URL, Authority: "registry", Publication: "draft"})
		if err != nil {
			t.Fatal(err)
		}
		remote := live.ToolClient(client.Blobs)
		result, err := Materialize(t.Context(), records, ref, filepath.Join(t.TempDir(), "agent"), Options{Tools: &remote})
		if err != nil {
			t.Fatal(err)
		}
		if result.Manifest.Sources[5].Publication != "draft" {
			t.Fatal("tool publication absent")
		}
		if err := live.Publish(t.Context(), pkg); err == nil {
			t.Fatal("read-only adapter published")
		}
		// The record's publisher must own the package id and match its
		// provenance; each mismatch alone is refused.
		for name, mutate := range map[string]func(map[string]any){
			// Publisher and provenance agree with each other but do not own the id.
			"publisher": func(p map[string]any) {
				prov := pkg.Provenance
				prov.Publisher = "msg://agent/test/agt_other"
				p["publisher"], p["provenance"] = "agt_other", prov
			},
			"provenance-publisher": func(p map[string]any) {
				prov := pkg.Provenance
				prov.Publisher = "msg://agent/test/agt_other"
				p["provenance"] = prov
			},
		} {
			t.Run(name, func(t *testing.T) {
				bad := maps.Clone(payload)
				mutate(bad)
				raw, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(liveRecord{ID: pkg.ID, Kind: "tool-package", Schema: "tool-package/1", Revision: "package-r1", Publication: "draft", Payload: raw})
				}))
				defer server.Close()
				live, err := NewLive(LiveConfig{URL: server.URL, Authority: "registry", Publication: "draft"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := live.Resolve(t.Context(), pkg.ID); err == nil || !strings.Contains(err.Error(), "package publisher mismatch") {
					t.Fatalf("publisher mismatch not refused: %v", err)
				}
			})
		}
	})
	t.Run("tool authority", func(t *testing.T) {
		foreign := append(Fake(nil), records...)
		var body map[string]any
		if err := json.Unmarshal(foreign[0].Payload, &body); err != nil {
			t.Fatal(err)
		}
		body["tools"] = []Reference{{Authority: "tether", Kind: "tool-package", ID: pkg.ID}}
		foreign[0].Payload, _ = json.Marshal(body)
		if _, err := Materialize(t.Context(), foreign, ref, filepath.Join(t.TempDir(), "agent"), Options{Tools: &client}); err == nil || !strings.Contains(err.Error(), "tool package authority does not match") {
			t.Fatal("foreign tool authority was not rejected by the authority guard", err)
		}
	})
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

func TestSymlinkDestinationRejectedBeforeResolution(t *testing.T) {
	records, ref := fixture(t)
	parent := t.TempDir()
	target := filepath.Join(parent, "real")
	if _, err := Materialize(t.Context(), records, ref, target); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(target, "agent.md")) // #nosec G304 -- target is created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "alias")
	// Keep the target inside os.Root: its traversal protection must not mask
	// removal of the materializer's destination guard.
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}
	calls := 0
	resolver := resolverFunc(func(ctx context.Context, ref Reference, revision string) (Record, error) {
		calls++
		return records.Get(ctx, ref, revision)
	})
	_, err = Materialize(t.Context(), resolver, ref, link, Options{Force: true})
	if err == nil || !strings.Contains(err.Error(), "destination must be a real directory") {
		t.Fatal("symlink not rejected at destination guard", err)
	}
	if calls != 0 {
		t.Fatal("resolved inputs through a symlink destination")
	}
	if destination, err := os.Readlink(link); err != nil || destination != "real" {
		t.Fatal("destination link changed", err)
	}
	after, err := os.ReadFile(filepath.Join(target, "agent.md")) // #nosec G304 -- target is created under t.TempDir.
	if err != nil || string(after) != string(before) {
		t.Fatal("symlink target changed", err)
	}
}

func TestFetchRejectsResolverIdentity(t *testing.T) {
	records, ref := fixture(t)
	for _, field := range []string{"authority", "kind", "id"} {
		t.Run(field, func(t *testing.T) {
			record := records[0]
			// Payload and revision still match the requested record. Only the returned
			// envelope is wrong, so payload/schema validation cannot catch the mutation.
			switch field {
			case "authority":
				record.Reference.Authority = "tether"
			case "kind":
				record.Reference.Kind = "skill"
			case "id":
				record.Reference.ID = "another"
			}
			resolver := resolverFunc(func(context.Context, Reference, string) (Record, error) { return record, nil })
			if _, _, _, err := fetch(t.Context(), resolver, ref, "r1"); err == nil || !strings.Contains(err.Error(), "resolver returned a different identity or revision") {
				t.Fatal("wrong resolver identity accepted", err)
			}
		})
	}
}

func TestFetchRejectsResolverRevision(t *testing.T) {
	records, ref := fixture(t)
	for _, revision := range []string{"", "r2"} {
		t.Run("returned="+revision, func(t *testing.T) {
			record := records[0]
			record.Revision = revision
			requested := "r1"
			if revision == "" {
				requested = "" // A latest lookup still requires a returned revision.
			}
			// Do not delegate to Fake.Get: it would reject the requested pin before
			// the fetch boundary could check the resolver's returned revision.
			resolver := resolverFunc(func(context.Context, Reference, string) (Record, error) { return record, nil })
			if _, _, _, err := fetch(t.Context(), resolver, ref, requested); err == nil || !strings.Contains(err.Error(), "resolver returned a different identity or revision") {
				t.Fatal("wrong resolver revision accepted", err)
			}
		})
	}
}

// fetch distrusts the resolver's payload as well as its envelope: a payload
// for another record, or credential-like reference metadata, is refused even
// when the envelope identity and revision are right.
func TestFetchRejectsResolverPayloadAndMetadata(t *testing.T) {
	records, ref := fixture(t)
	// Built at runtime so the source carries no literal token pattern.
	token := "ghp_" + strings.Repeat("A1b2", 9)
	for _, tc := range []struct{ name, requested, want string }{
		{"payload-id", "r1", "payload identity mismatch"},
		{"credential-revision", "", "credential-like reference metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := records[0]
			switch tc.name {
			case "payload-id":
				record.Payload = []byte(strings.Replace(string(record.Payload), `"id":"fixture"`, `"id":"other"`, 1))
			case "credential-revision":
				record.Revision = token
			}
			resolver := resolverFunc(func(context.Context, Reference, string) (Record, error) { return record, nil })
			_, _, _, err := fetch(t.Context(), resolver, ref, tc.requested)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatal("error leaks the credential-like value")
			}
		})
	}
	// Control: the unmodified record passes the same boundary.
	resolver := resolverFunc(func(context.Context, Reference, string) (Record, error) { return records[0], nil })
	if _, _, _, err := fetch(t.Context(), resolver, ref, "r1"); err != nil {
		t.Fatal(err)
	}
}
