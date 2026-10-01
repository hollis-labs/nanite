package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// CW-20261001-0172: two concurrent session creates picked the same short
// code. NextShortCode read the highest cN and CreateSession inserted later,
// with nothing atomic between, so the second insert failed "UNIQUE
// constraint failed: sessions.short_code".
func TestCreateSession_ConcurrentCreatesGetDistinctShortCodes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// A session that already exists, so allocation continues past it.
	first := &Session{Title: "existing"}
	if err := s.CreateSession(ctx, first); err != nil {
		t.Fatalf("CreateSession(existing): %v", err)
	}

	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	sessions := make([]*Session, n)
	for i := 0; i < n; i++ {
		sessions[i] = &Session{Title: fmt.Sprintf("concurrent %d", i)}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = s.CreateSession(ctx, sessions[i])
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[string]int{first.ShortCode: -1}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
		code := sessions[i].ShortCode
		if prev, dup := seen[code]; dup {
			t.Fatalf("sessions %d and %d both got short code %q", prev, i, code)
		}
		seen[code] = i
		got, err := s.GetSession(ctx, sessions[i].ID)
		if err != nil {
			t.Fatalf("GetSession %d: %v", i, err)
		}
		if got.ShortCode != code {
			t.Fatalf("session %d stored short code %q, returned %q", i, got.ShortCode, code)
		}
	}
	// Codes stay dense: c1 for the existing session, then c2..c(n+1).
	for want := 1; want <= n+1; want++ {
		if _, ok := seen[fmt.Sprintf("c%d", want)]; !ok {
			t.Fatalf("short code c%d was never allocated; got %v", want, seen)
		}
	}
}

// ForkSession allocates its short code the same way, so forks racing creates
// also get distinct codes.
func TestForkSession_RacingCreatesGetDistinctShortCodes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	src := &Session{Title: "source"}
	if err := s.CreateSession(ctx, src); err != nil {
		t.Fatalf("CreateSession(source): %v", err)
	}

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, 2*n)
	codes := make([]string, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			<-start
			forked, err := s.ForkSession(ctx, src.ID, nil, false)
			errs[i] = err
			if err == nil {
				codes[i] = forked.ShortCode
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			<-start
			sess := &Session{Title: fmt.Sprintf("create %d", i)}
			errs[n+i] = s.CreateSession(ctx, sess)
			codes[n+i] = sess.ShortCode
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[string]int{src.ShortCode: -1}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("create/fork %d: %v", i, err)
		}
		if prev, dup := seen[codes[i]]; dup {
			t.Fatalf("%d and %d both got short code %q", prev, i, codes[i])
		}
		seen[codes[i]] = i
	}
}
