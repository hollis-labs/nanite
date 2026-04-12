package coordination

import (
	"context"
	"log"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/hollis-labs/nanite/internal/lifecycle"
)

// BadgerStore implements CoordStore using Badger v4.
type BadgerStore struct {
	db        *badger.DB
	lifecycle *lifecycle.Manager
}

// NewBadgerStore opens a Badger database at dir and starts a background
// value log GC goroutine.
func NewBadgerStore(dir string) (*BadgerStore, error) {
	opts := badger.DefaultOptions(dir).
		WithLogger(nil). // suppress Badger's own logging
		WithNumVersionsToKeep(1).
		WithCompactL0OnClose(true)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}

	s := &BadgerStore{
		db:        db,
		lifecycle: lifecycle.NewManager("coordination.badger"),
	}
	s.lifecycle.Go("gc", s.gcLoop)
	return s, nil
}

func (s *BadgerStore) Put(key string, value []byte, ttl time.Duration) error {
	return s.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte(key), value)
		if ttl > 0 {
			e = e.WithTTL(ttl)
		}
		return txn.SetEntry(e)
	})
}

func (s *BadgerStore) Get(key string) ([]byte, error) {
	var val []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err == badger.ErrKeyNotFound {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		return err
	})
	return val, err
}

func (s *BadgerStore) Delete(key string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete([]byte(key))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		return err
	})
}

func (s *BadgerStore) List(prefix string) ([]KVEntry, error) {
	var entries []KVEntry
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefix)); it.Valid(); it.Next() {
			item := it.Item()
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			entry := KVEntry{
				Key:   string(item.Key()),
				Value: val,
			}
			if exp := item.ExpiresAt(); exp > 0 {
				entry.ExpiresAt = time.Unix(int64(exp), 0)
			}
			entries = append(entries, entry)
		}
		return nil
	})
	return entries, err
}

func (s *BadgerStore) Available() bool { return true }

func (s *BadgerStore) Close() error {
	// Cancel and wait for the GC loop before closing the DB. Closing the DB
	// while gcLoop is mid-RunValueLogGC would race; lifecycle.Shutdown
	// guarantees the goroutine has exited before we proceed.
	_ = s.lifecycle.Shutdown(10 * time.Second)
	return s.db.Close()
}

// gcLoop runs Badger's value log garbage collection periodically.
func (s *BadgerStore) gcLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				if err := s.db.RunValueLogGC(0.5); err != nil {
					break // nothing more to GC
				}
				log.Println("coordination: badger value log GC completed")
			}
		}
	}
}
