package slots

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sync"
)

type Name string

const (
	Universal    Name = "universal"
	System       Name = "system"
	Memory       Name = "memory"
	Agent        Name = "agent"
	Mode         Name = "mode"
	Rules        Name = "rules"
	Permissions  Name = "permissions"
	Workspace    Name = "workspace"
	Skills       Name = "skills"
	Tools        Name = "tools"
	Session      Name = "session"
	Context      Name = "context"
	UserContext  Name = "user_context"
	Handoff      Name = "handoff"
	Conversation Name = "conversation"
)

// Names returns the canonical slot order in a new slice.
func Names() []Name {
	return []Name{Universal, System, Memory, Agent, Mode, Rules, Permissions, Workspace, Skills, Tools, Session, Context, UserContext, Handoff, Conversation}
}

func HostWritable(n Name) bool {
	switch n {
	case System, Memory, Mode, Rules, Workspace, Skills, Tools, Session, Context, UserContext:
		return true
	default:
		return false
	}
}

type Entry struct {
	Owner, Key, Content, Pointer string
	Priority                     int
	Labels                       map[string]string
}

type ownerKey struct{ owner, key string }

type Store struct {
	mu      sync.RWMutex
	entries map[Name]map[ownerKey]Entry
	version uint64
}

func NewStore() *Store { return &Store{entries: make(map[Name]map[ownerKey]Entry)} }

func validate(slot Name, e Entry) error {
	if !slices.Contains(Names(), slot) {
		return fmt.Errorf("unknown slot %q", slot)
	}
	if e.Owner == "" || e.Key == "" {
		return fmt.Errorf("slot entry requires owner and key")
	}
	if !HostWritable(slot) && e.Owner != "engine" && e.Owner != "file" {
		return fmt.Errorf("slot %q is reserved", slot)
	}
	return nil
}

func (s *Store) Put(slot Name, e Entry) error {
	if err := validate(slot, e); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Labels = maps.Clone(e.Labels)
	s.bucket(slot)[ownerKey{e.Owner, e.Key}] = e
	s.version++
	return nil
}

func (s *Store) Replace(slot Name, owner string, entries ...Entry) error {
	if err := validate(slot, Entry{Owner: owner, Key: "_"}); err != nil {
		return err
	}
	next := make(map[ownerKey]Entry, len(entries))
	for _, e := range entries {
		e.Owner = owner
		if err := validate(slot, e); err != nil {
			return err
		}
		id := ownerKey{owner, e.Key}
		if _, exists := next[id]; exists {
			return fmt.Errorf("duplicate slot entry key %q", e.Key)
		}
		e.Labels = maps.Clone(e.Labels)
		next[id] = e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.bucket(slot)
	maps.DeleteFunc(bucket, func(id ownerKey, _ Entry) bool { return id.owner == owner })
	maps.Copy(bucket, next)
	s.version++
	return nil
}

func (s *Store) bucket(slot Name) map[ownerKey]Entry {
	if s.entries[slot] == nil {
		s.entries[slot] = make(map[ownerKey]Entry)
	}
	return s.entries[slot]
}

func (s *Store) Remove(slot Name, owner, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := ownerKey{owner, key}
	if _, exists := s.entries[slot][id]; exists {
		delete(s.entries[slot], id)
		s.version++
	}
}

func (s *Store) ClearOwner(owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, bucket := range s.entries {
		maps.DeleteFunc(bucket, func(id ownerKey, _ Entry) bool { return id.owner == owner })
	}
	s.version++
}

type Snapshot struct {
	entries map[Name][]Entry
	version uint64
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{entries: make(map[Name][]Entry, len(s.entries)), version: s.version}
	for slot, bucket := range s.entries {
		entries := slices.Collect(maps.Values(bucket))
		slices.SortFunc(entries, func(a, b Entry) int {
			if order := cmp.Compare(b.Priority, a.Priority); order != 0 {
				return order
			}
			if order := cmp.Compare(a.Owner, b.Owner); order != 0 {
				return order
			}
			return cmp.Compare(a.Key, b.Key)
		})
		out.entries[slot] = entries
	}
	return out
}

func (s Snapshot) Entries(slot Name) []Entry {
	entries := slices.Clone(s.entries[slot])
	for i := range entries {
		entries[i].Labels = maps.Clone(entries[i].Labels)
	}
	return entries
}

func (s Snapshot) Version() uint64 { return s.version }

// With applies updates to an independent snapshot.
func (s Snapshot) With(updates ...Update) (Snapshot, error) {
	if len(updates) == 0 {
		return s, nil
	}
	out := s.copyStore()
	for _, update := range updates {
		if err := out.Put(update.Slot, update.Entry); err != nil {
			return Snapshot{}, err
		}
	}
	return out.Snapshot(), nil
}

func (s Snapshot) copyStore() *Store {
	out := NewStore()
	out.version = s.version
	for name, entries := range s.entries {
		for _, e := range entries {
			out.bucket(name)[ownerKey{e.Owner, e.Key}] = e
		}
	}
	return out
}
