package coordination

import "time"

// NoopStore is a no-op CoordStore used when Badger fails to initialize.
// Multi-agent coordination features are disabled but the app starts normally.
type NoopStore struct{}

// NewNoopStore returns a no-op coordination store.
func NewNoopStore() *NoopStore { return &NoopStore{} }

func (n *NoopStore) Put(key string, value []byte, ttl time.Duration) error { return nil }
func (n *NoopStore) Get(key string) ([]byte, error)                       { return nil, ErrNotFound }
func (n *NoopStore) Delete(key string) error                              { return nil }
func (n *NoopStore) List(prefix string) ([]KVEntry, error)                { return nil, nil }
func (n *NoopStore) Available() bool                                      { return false }
func (n *NoopStore) Close() error                                         { return nil }
