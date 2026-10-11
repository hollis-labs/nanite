package apiauth

import (
	"context"
	"sync"
)

// registry tracks the cancel function of every in-flight authenticated
// request by credential id and UI session id, so revoking a credential or
// logging out closes its open streams at once (CW-20261011-0036 decision 10).
type registry struct {
	mu    sync.Mutex
	byKey map[string]map[*entry]struct{}
}

type entry struct{ cancel context.CancelFunc }

func newRegistry() *registry { return &registry{byKey: map[string]map[*entry]struct{}{}} }

// add registers cancel under keys and returns a function that unregisters it.
func (r *registry) add(cancel context.CancelFunc, keys ...string) func() {
	e := &entry{cancel: cancel}
	r.mu.Lock()
	for _, k := range keys {
		if k == "" {
			continue
		}
		if r.byKey[k] == nil {
			r.byKey[k] = map[*entry]struct{}{}
		}
		r.byKey[k][e] = struct{}{}
	}
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		for _, k := range keys {
			if set := r.byKey[k]; set != nil {
				delete(set, e)
				if len(set) == 0 {
					delete(r.byKey, k)
				}
			}
		}
		r.mu.Unlock()
	}
}

// cancelKey cancels every request registered under key.
func (r *registry) cancelKey(key string) {
	r.mu.Lock()
	var cancels []context.CancelFunc
	for e := range r.byKey[key] {
		cancels = append(cancels, e.cancel)
	}
	r.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}
