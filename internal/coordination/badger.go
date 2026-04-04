package coordination

import (
	"log"
	"time"

	badger "github.com/dgraph-io/badger/v4"
)

// BadgerStore implements CoordStore using Badger v4.
type BadgerStore struct {
	db     *badger.DB
	stopGC chan struct{}
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
		db:     db,
		stopGC: make(chan struct{}),
	}
	go s.gcLoop()
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
	close(s.stopGC)
	return s.db.Close()
}

// gcLoop runs Badger's value log garbage collection periodically.
func (s *BadgerStore) gcLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopGC:
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
