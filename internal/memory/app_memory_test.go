package memory

import (
	"context"
	"testing"
)

func TestAppMemoryAccessDoesNotTrustTagsOrGlobalRecall(t *testing.T) {
	instance, closeInstance := newTestTesseract(t)
	defer closeInstance()
	svc := NewService(instance.MemoryStore())
	ctx := context.Background()
	ns := AppNamespace()
	for _, user := range []string{"default", "alice"} {
		value := Memory{Namespace: ns, MemoryKey: AppMemoryKey(user, "same_key"), Summary: "private " + user, Body: "body " + user, Confidence: 0.9, Status: "reviewed", Tags: []string{"user:default", "user:alice"}}
		if err := svc.StoreAppForUser(ctx, user, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Recall(ctx, RecallOpts{}); err == nil {
		t.Fatal("unscoped recall unexpectedly accepted")
	}
	for _, selector := range []string{AppMemoryPrefix(), "app/*", "app/nanite/*", AppMemoryPrefix() + "/*", ns} {
		rows, err := svc.Recall(ctx, RecallOpts{Namespaces: []string{selector}, Ranking: RankingChronological, PayloadMode: PayloadModeFull})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Summary != "private default" {
			t.Fatalf("selector %s did not return only the bound user: %+v", selector, rows)
		}
		if _, err := svc.RecallPage(ctx, RecallOpts{Namespaces: []string{selector}}); err == nil {
			t.Fatalf("selector %s bypassed app filter through cursor paging", selector)
		}
	}
	own, err := svc.Get(ctx, ns, AppMemoryKey("default", "same_key"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := instance.MemoryStore().GetCurrent(ctx, ns, AppMemoryKey("alice", "same_key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, ns, foreign.MemoryKey); err == nil {
		t.Fatal("foreign exact lookup accepted")
	}
	if _, err := svc.GetRevision(ctx, foreign.RevisionID); err == nil {
		t.Fatal("foreign revision hydrated")
	}
	if err := svc.Touch(ctx, []string{own.RevisionID, foreign.RevisionID}); err == nil {
		t.Fatal("mixed ownership touch accepted")
	}
	if err := svc.Deprecate(ctx, foreign.RevisionID); err == nil {
		t.Fatal("foreign status effect accepted")
	}
	if err := svc.StoreAppForUser(ctx, "default", Memory{Namespace: ns, MemoryKey: foreign.MemoryKey, Summary: "overwrite", Confidence: 0.9}); err == nil {
		t.Fatal("foreign logical key overwritten")
	}
	if err := svc.Store(ctx, Memory{Namespace: ns, MemoryKey: AppMemoryKey("default", "forged"), Origin: "user", Trigger: "manual", Summary: "authority metadata", Confidence: 0.9}); err == nil {
		t.Fatal("generic writer gained app authority")
	}
}
