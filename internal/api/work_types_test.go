package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWorkWireCRUD(t *testing.T) {
	for _, resource := range []string{"plans", "todos"} {
		t.Run(resource, func(t *testing.T) {
			_, mux := newTestAPI(t)
			body := map[string]any{"id": "wire-" + resource, "title": "Wire title", "description": "Wire description", "scope": "session", "scope_id": "wire-session", "metadata": `{"source":"wire"}`, "created_by": "wire-client"}
			if resource == "plans" {
				body["steps"] = `[{"id":"step-1","title":"First","status":"pending"}]`
			} else {
				body["priority"] = "high"
				body["labels"] = `["wire"]`
			}
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			created := mcpDo(mux, http.MethodPost, "/api/"+resource, string(payload))
			if created.Code != http.StatusCreated {
				t.Fatalf("create %s: %d %s", resource, created.Code, created.Body.String())
			}
			var row map[string]any
			if decodeErr := json.Unmarshal(created.Body.Bytes(), &row); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			for key, want := range body {
				if row[key] != want {
					t.Fatalf("create %s lost %s: got %#v, want %#v", resource, key, row[key], want)
				}
			}
			if row["created_at"] == nil || row["created_at"] == "" {
				t.Fatalf("create %s lost timestamp: %#v", resource, row)
			}
			updated := mcpDo(mux, http.MethodPut, "/api/"+resource+"/wire-"+resource, `{"title":"Changed title"}`)
			if updated.Code != http.StatusOK {
				t.Fatalf("update %s: %d %s", resource, updated.Code, updated.Body.String())
			}
			got := mcpDo(mux, http.MethodGet, "/api/"+resource+"/wire-"+resource, "")
			if got.Code != http.StatusOK {
				t.Fatalf("get %s: %d %s", resource, got.Code, got.Body.String())
			}
			if decodeErr := json.Unmarshal(got.Body.Bytes(), &row); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if row["title"] != "Changed title" || row["description"] != "Wire description" || row["scope_id"] != "wire-session" {
				t.Fatalf("get %s changed wire attributes: %#v", resource, row)
			}
		})
	}
}
