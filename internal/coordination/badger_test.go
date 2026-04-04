package coordination

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *BadgerStore {
	t.Helper()
	s, err := NewBadgerStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBadgerStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBadgerPutGet(t *testing.T) {
	s := newTestStore(t)

	if err := s.Put("key1", []byte("value1"), 0); err != nil {
		t.Fatalf("Put: %v", err)
	}

	val, err := s.Get("key1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("got %q, want %q", val, "value1")
	}
}

func TestBadgerGetNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Get("nonexistent")
	if err != ErrNotFound {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestBadgerDelete(t *testing.T) {
	s := newTestStore(t)

	_ = s.Put("key1", []byte("value1"), 0)
	if err := s.Delete("key1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Get("key1")
	if err != ErrNotFound {
		t.Errorf("after delete: got %v, want ErrNotFound", err)
	}
}

func TestBadgerDeleteNonexistent(t *testing.T) {
	s := newTestStore(t)

	if err := s.Delete("nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestBadgerList(t *testing.T) {
	s := newTestStore(t)

	_ = s.Put("task:1", []byte("t1"), 0)
	_ = s.Put("task:2", []byte("t2"), 0)
	_ = s.Put("agent:a", []byte("a1"), 0)

	entries, err := s.List("task:")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestBadgerTTLExpiry(t *testing.T) {
	s := newTestStore(t)

	// Badger TTL resolution is 1 second, so use 2s TTL.
	if err := s.Put("ephemeral", []byte("gone"), 2*time.Second); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Should exist immediately.
	val, err := s.Get("ephemeral")
	if err != nil {
		t.Fatalf("Get before expiry: %v", err)
	}
	if string(val) != "gone" {
		t.Errorf("got %q, want %q", val, "gone")
	}

	// Wait for TTL to expire.
	time.Sleep(3 * time.Second)

	_, err = s.Get("ephemeral")
	if err != ErrNotFound {
		t.Errorf("after TTL: got %v, want ErrNotFound", err)
	}
}

func TestBadgerAvailable(t *testing.T) {
	s := newTestStore(t)
	if !s.Available() {
		t.Error("BadgerStore should be available")
	}
}

func TestBadgerConcurrentWrites(t *testing.T) {
	s := newTestStore(t)

	// Simulate two goroutines writing heartbeats concurrently.
	done := make(chan error, 2)
	write := func(prefix string, count int) {
		for i := 0; i < count; i++ {
			key := prefix + string(rune('0'+i))
			if err := s.Put(key, []byte("hb"), HeartbeatTTL); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}

	go write("agent:a:hb:", 50)
	go write("agent:b:hb:", 50)

	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
}

func TestNoopStore(t *testing.T) {
	s := NewNoopStore()

	if s.Available() {
		t.Error("NoopStore should not be available")
	}
	if err := s.Put("k", []byte("v"), 0); err != nil {
		t.Errorf("Put: %v", err)
	}
	_, err := s.Get("k")
	if err != ErrNotFound {
		t.Errorf("Get: got %v, want ErrNotFound", err)
	}
	if err := s.Delete("k"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	entries, err := s.List("k")
	if err != nil {
		t.Errorf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List: got %d, want 0", len(entries))
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
