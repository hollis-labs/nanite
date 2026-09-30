package service

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

type envelopeLookupCtxKey struct{}

// fakeEnvelopeInstances serves instances by ID, fails the IDs in errs, and
// records each fetch and the ctx value it ran under.
type fakeEnvelopeInstances struct {
	rows    map[string]*store.EnvelopeInstance
	errs    map[string]error
	fetched []string
	ctxVals []any
}

func (f *fakeEnvelopeInstances) GetEnvelopeInstance(ctx context.Context, id string) (*store.EnvelopeInstance, error) {
	f.fetched = append(f.fetched, id)
	f.ctxVals = append(f.ctxVals, ctx.Value(envelopeLookupCtxKey{}))
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	if inst, ok := f.rows[id]; ok {
		return inst, nil
	}
	return nil, sql.ErrNoRows
}

func TestSessionEnvelopeLookup(t *testing.T) {
	fake := &fakeEnvelopeInstances{
		rows: map[string]*store.EnvelopeInstance{
			"one":   {ID: "one"},
			"two":   {ID: "two"},
			"three": {ID: "three"},
		},
		errs: map[string]error{"broken": errors.New("db down")},
	}
	svc := NewSessionService(SessionServiceDeps{EnvelopeInstances: fake})
	ctx := context.WithValue(context.Background(), envelopeLookupCtxKey{}, "caller")

	msgs := []store.Message{
		{Envelope: `{"id":"one"}`},
		{Envelope: ` [{"id":"two"},{"id":"one"},{"id":""}]`}, // array; "one" again is not refetched
		{Envelope: `{"id":"missing"}`},                       // ErrNoRows: skipped
		{Envelope: `{"id":"broken"}`},                        // other error: skipped
		{Envelope: `{not json`},                              // skipped
		{Envelope: "   "},                                    // blank: skipped
		{},                                                   // no envelope
		{Envelope: `[{"id":"three"}]`},
	}
	got := svc.EnvelopeLookup(ctx, msgs)

	keys := make([]string, 0, len(got))
	for k, v := range got {
		if v.ID != k {
			t.Errorf("lookup[%q] holds instance %q", k, v.ID)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"one", "three", "two"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("lookup keys = %v, want %v", keys, want)
	}
	if want := []string{"one", "two", "missing", "broken", "three"}; !reflect.DeepEqual(fake.fetched, want) {
		t.Fatalf("fetched = %v, want %v", fake.fetched, want)
	}
	for i, v := range fake.ctxVals {
		if v != "caller" {
			t.Fatalf("fetch %d ran under a ctx without the caller's value", i)
		}
	}
}

func TestSessionEnvelopeLookup_NoDependency(t *testing.T) {
	got := NewSessionService(SessionServiceDeps{}).EnvelopeLookup(context.Background(), []store.Message{{Envelope: `{"id":"one"}`}})
	if got == nil || len(got) != 0 {
		t.Fatalf("lookup = %v, want an empty non-nil map", got)
	}
}
