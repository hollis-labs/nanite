package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	toolresult "github.com/hollis-labs/go-toolresult"
)

func TestResultProjection_Availability(t *testing.T) {
	for _, state := range []string{"live", "expired", "purged", "oversize", "foreign", "offline", "canceled"} {
		t.Run(state, func(t *testing.T) {
			_, db := setupTestCache(t)
			defer db.Close()
			cache := NewResultCache(db, ResultCacheConfig{})
			body := strings.Repeat("retained source detail\n", 400)
			if state == "oversize" {
				body = strings.Repeat("x", (1<<20)+1)
			}
			ctx := context.Background()
			view, err := cache.Results.Present(ctx, "owner", toolresult.Meta{CallID: "source", Tool: "read_source"}, body, 500)
			if err != nil {
				t.Fatal(err)
			}
			if time.Until(view.Pointer.ExpiresAt) < 59*time.Minute {
				t.Fatal("default one-hour TTL was shortened")
			}
			switch state {
			case "expired":
				if _, err = db.Exec(`UPDATE tool_result_cache SET expires_at = '2020-01-01T00:00:00Z'`); err != nil {
					t.Fatal(err)
				}
			case "purged":
				if _, err = db.Exec(`DELETE FROM tool_result_cache`); err != nil {
					t.Fatal(err)
				}
			case "offline":
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			scope := "owner"
			if state == "foreign" {
				scope = "other"
			}
			projection := NewResultProjection(ctx, cache.Results, scope)
			got := projection.Text(view.Content)
			if state == "live" {
				if got != view.Content || projection.Notices() != "" {
					t.Fatal("live pointer changed")
				}
				page, readErr := cache.Results.Read(ctx, scope, view.CacheID, "", 0, 100, 500)
				if readErr != nil || page.Content != body[:100] {
					t.Fatalf("live body unavailable: %v", readErr)
				}
				return
			}
			if strings.Contains(got, "tool_result://") || strings.Contains(got, `Use fetch_tool_result({`) || !strings.Contains(got, "CACHED RESULT UNAVAILABLE") {
				t.Fatalf("stale recovery promise survived: %s", got)
			}
			if !strings.Contains(got, strings.Split(view.Content, "\n\n[TRUNCATED")[0]) {
				t.Fatal("preview changed")
			}
			if again := projection.Text(got); again != got {
				t.Fatal("repeated projection changed unavailable view")
			}
			if state == "oversize" && !strings.Contains(got, "per-result storage cap") {
				t.Fatal("oversize body presented as TTL expiry")
			}
			if state == "offline" || state == "canceled" {
				if !strings.Contains(got, "could not be checked") {
					t.Fatal("unknown availability reported as expiry")
				}
			}
			// The stored body/metadata is never rewritten by reconciliation.
			if state != "offline" && state != "purged" {
				var original *string
				if err = db.QueryRow(`SELECT body FROM tool_result_cache WHERE id = ?`, view.CacheID).Scan(&original); err != nil {
					t.Fatal(err)
				}
				if state == "oversize" {
					if original != nil {
						t.Fatal("oversized full body was stored")
					}
				} else if original == nil || *original != body {
					t.Fatal("cached authority changed")
				}
			}
		})
	}
}

func TestResultProjection_MultipleReferencesAndNilCache(t *testing.T) {
	projection := NewResultProjection(context.Background(), nil, "owner")
	input := "quoted tool_result://first and tool_result://second; again tool_result://first"
	got := projection.Text(input)
	if strings.Contains(got, "tool_result://") || !strings.Contains(got, "first;") || !strings.Contains(got, "second;") {
		t.Fatal(got)
	}
	if strings.Count(projection.Notices(), "CACHED RESULT UNAVAILABLE") != 2 {
		t.Fatal("corrections were not deduplicated")
	}
}
