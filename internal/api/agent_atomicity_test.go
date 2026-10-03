package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func atomicAgentRequest(t *testing.T, h http.Handler, method, path string, body map[string]any, want int) *store.AgentProfile {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d %s, want %d", method, path, w.Code, w.Body.String(), want)
	}
	if want != 200 && want != 201 {
		var wire map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		message, ok := wire["error"].(string)
		if !ok || len(wire) != 1 || message == "" || strings.Contains(message, "write_secret") || strings.Contains(message, "SQLITE") {
			t.Fatalf("unsafe error response: %s", w.Body.String())
		}
		return nil
	}
	var p store.AgentProfile
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func atomicAgentBody(slug, role string) map[string]any {
	return map[string]any{"name": "Atomic Agent", "slug": slug, "system_prompt": "fixture", "role_id": role, "consumer_id": "blt-loom-001", "protocol": "acp", "transport": "stdio", "role_tools": `["atomic-fixture-tool"]`, "role_skills": `["atomic-skill"]`}
}

func TestAgentWritesAtomic(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		for _, failure := range []string{"protocol", "transport", "role_id", "consumer_id", "model_id", "duplicate slug", "trust write", "assignment write", "protocol write", "seed write", "grant write", "skill write", "saved read", "foreign key during write"} {
			if method == "PUT" && failure == "trust write" {
				continue
			}
			t.Run(method+"/"+failure, func(t *testing.T) {
				st, h := serviceErrorRoutes(t)
				toolID, seedErr := st.UpsertKnownTool(t.Context(), "atomic-fixture-tool", "internal", "active", "fixture")
				if seedErr != nil {
					t.Fatal(seedErr)
				}
				if err := st.CreateSkill(t.Context(), &store.Skill{Name: "Atomic Skill", Slug: "atomic-skill"}); err != nil {
					t.Fatal(err)
				}
				role := &store.Role{Name: "Fixture Role", Slug: "atomic-role", SystemPrompt: "fixture"}
				if err := st.CreateRole(t.Context(), role); err != nil {
					t.Fatal(err)
				}
				body := atomicAgentBody("atomic-agent", role.ID)
				path := "/api/agents"
				var before *store.AgentProfile
				if method == "PUT" {
					before = atomicAgentRequest(t, h, "POST", path, map[string]any{"name": "Original", "slug": "original-agent", "system_prompt": "original"}, 201)
					// Read the raw persisted profile instead of comparing API-only view fields.
					var err error
					before, err = st.GetAgent(t.Context(), before.ID)
					if err != nil {
						t.Fatal(err)
					}
					path += "/" + before.ID
				}
				want := 400
				trigger := ""
				switch failure {
				case "protocol":
					body["protocol"] = "made-up"
				case "transport":
					body["transport"] = "made-up"
				case "role_id", "consumer_id", "model_id":
					body[failure] = "missing"
				case "duplicate slug":
					atomicAgentRequest(t, h, "POST", "/api/agents", map[string]any{"name": "Taken", "slug": "taken-agent", "system_prompt": "fixture"}, 201)
					body["slug"] = "taken-agent"
					want = 409
				case "trust write":
					trigger = `BEFORE UPDATE OF default_trust_tier ON agent_profiles`
					want = 500
				case "assignment write":
					trigger = `BEFORE UPDATE OF role_id ON agent_profiles WHEN NEW.role_id IS NOT OLD.role_id`
					want = 500
				case "protocol write":
					trigger = `BEFORE UPDATE OF protocol ON agent_profiles WHEN NEW.protocol IS NOT OLD.protocol`
					want = 500
				case "seed write":
					body["role_tools"] = `["atomic-fixture-tool","second-failing-tool"]`
					trigger = `BEFORE INSERT ON agent_known_tools WHEN NEW.tool_name='second-failing-tool'`
					want = 500
				case "grant write":
					trigger = `BEFORE INSERT ON agent_tools`
					want = 500
				case "skill write":
					trigger = `BEFORE INSERT ON agent_known_skills`
					want = 500
				case "saved read":
					if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_atomic_write AFTER UPDATE OF protocol ON agent_profiles WHEN NEW.protocol IS NOT OLD.protocol BEGIN UPDATE agent_profiles SET tether_managed='invalid-bool' WHERE id=NEW.id; END`); err != nil {
						t.Fatal(err)
					}
					want = 500
				case "foreign key during write":
					if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_atomic_write AFTER UPDATE OF role_id ON agent_profiles WHEN NEW.role_id IS NOT OLD.role_id BEGIN UPDATE agent_profiles SET role_id='missing-after-validation' WHERE id=NEW.id; END`); err != nil {
						t.Fatal(err)
					}
				}
				if trigger != "" {
					if _, err := st.DB.ExecContext(t.Context(), "CREATE TRIGGER fail_atomic_write "+trigger+" BEGIN SELECT RAISE(ABORT,'write_secret private agent query'); END"); err != nil {
						t.Fatal(err)
					}
				}
				atomicAgentRequest(t, h, method, path, body, want)
				var children int
				if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_known_tools WHERE tool_name IN ('atomic-fixture-tool','second-failing-tool')`).Scan(&children); err != nil {
					t.Fatal(err)
				}
				var grants, skills int
				if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_tools WHERE tool_id=?`, toolID).Scan(&grants); err != nil {
					t.Fatal(err)
				}
				if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_known_skills WHERE skill_name='atomic-skill'`).Scan(&skills); err != nil {
					t.Fatal(err)
				}
				if grants != 0 || skills != 0 {
					t.Fatalf("failed request left grant/skill rows: %d/%d", grants, skills)
				}
				if children != 0 {
					t.Fatalf("failed request left child rows: %d", children)
				}
				var count int
				if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_profiles WHERE slug='atomic-agent'`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("failed request persisted an agent row: %d", count)
				}
				if before != nil {
					after, err := st.GetAgent(t.Context(), before.ID)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatalf("failed PUT modified persisted profile: before=%+v after=%+v", before, after)
					}
				}
				if failure == "duplicate slug" {
					var rows int
					if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_profiles WHERE slug='taken-agent'`).Scan(&rows); err != nil {
						t.Fatal(err)
					}
					if rows != 1 {
						t.Fatalf("duplicate request changed existing row count: %d", rows)
					}
				}
				if trigger != "" || failure == "foreign key during write" || failure == "saved read" {
					if _, err := st.DB.ExecContext(t.Context(), `DROP TRIGGER fail_atomic_write`); err != nil {
						t.Fatal(err)
					}
				}
				success := 201
				if method == "PUT" {
					success = 200
				}
				retry := atomicAgentRequest(t, h, method, path, atomicAgentBody("atomic-agent", role.ID), success)
				saved, err := st.GetAgentBySlug(t.Context(), "atomic-agent")
				if err != nil {
					t.Fatal(err)
				}
				if saved.ID != retry.ID || saved.RoleID != role.ID || saved.ConsumerID != "blt-loom-001" || saved.Protocol != "acp" || saved.Transport != "stdio" {
					t.Fatalf("corrected retry failed to persist assignments: %+v", saved)
				}
				if before != nil && saved.ID != before.ID {
					t.Fatalf("corrected update changed identity: %s -> %s", before.ID, saved.ID)
				}
				var granted int
				if grantErr := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_tools WHERE agent_id=? AND tool_id=?`, saved.ID, toolID).Scan(&granted); grantErr != nil || granted != 1 {
					t.Fatalf("corrected retry grant count=%d err=%v", granted, grantErr)
				}
				known, err := st.ListAgentKnownTools(t.Context(), saved.ID)
				if err != nil || len(known) != 1 {
					t.Fatalf("corrected retry seed: %+v err=%v", known, err)
				}
			})
		}
	}
}

func TestAgentInvalidAssignmentsValidateBeforeWrite(t *testing.T) {
	st, h := serviceErrorRoutes(t)
	// If client validation were postponed until after the first write, this
	// trigger would turn the 400 into a 500 (even with eventual rollback).
	if _, err := st.DB.ExecContext(t.Context(), `CREATE TRIGGER forbid_agent_insert BEFORE INSERT ON agent_profiles BEGIN SELECT RAISE(ABORT,'write_secret insertion before validation'); END`); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"protocol", "role_id", "consumer_id", "model_id"} {
		body := map[string]any{"name": "Invalid", "slug": "invalid-" + strings.ReplaceAll(field, "_", "-"), "system_prompt": "fixture", field: "missing"}
		atomicAgentRequest(t, h, "POST", "/api/agents", body, 400)
		_, err := st.GetAgentBySlug(t.Context(), body["slug"].(string))
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("invalid %s persisted: %v", field, err)
		}
	}
}

func TestAgentAssignmentsFailureMessageNamesFields(t *testing.T) {
	_, h := serviceErrorRoutes(t)
	for _, field := range []string{"protocol", "transport", "role_id", "consumer_id", "model_id"} {
		body := fmt.Sprintf(`{"name":"Invalid","slug":"invalid-%s","system_prompt":"fixture",%q:"missing"}`, strings.ReplaceAll(field, "_", "-"), field)
		message := serviceErrorRequest(t, h, "POST", "/api/agents", body, 400)
		if field == "protocol" || field == "transport" {
			if !strings.Contains(message, field) {
				t.Fatalf("field missing from error: %s", message)
			}
		} else if message != "agent assignment does not reference an existing role, consumer, or model" {
			t.Fatalf("assignment error contract changed: %s", message)
		}
	}
}

func TestAgentConcurrentCreateAddsOneProfile(t *testing.T) {
	st, h := serviceErrorRoutes(t)
	start := make(chan struct{})
	results := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/api/agents", strings.NewReader(`{"name":"Concurrent","slug":"concurrent-agent","system_prompt":"fixture"}`)))
			results[i] = w
		}()
	}
	close(start)
	wg.Wait()
	codes := map[int]int{}
	for _, w := range results {
		codes[w.Code]++
	}
	if codes[201] != 1 || codes[409] != 1 {
		t.Fatalf("concurrent create results: %d %s; %d %s", results[0].Code, results[0].Body.String(), results[1].Code, results[1].Body.String())
	}
	var count int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_profiles WHERE slug='concurrent-agent'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent persisted rows=%d err=%v", count, err)
	}
}
