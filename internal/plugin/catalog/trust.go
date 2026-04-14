package catalog

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TrustedKey describes a single entry in the trust ring. Keys carry an
// identifier (used to match catalog-declared signer_key_id fields), optional
// expiration (zero = never expires), and an optional revocation flag that
// disables the key without removing it from the ring (so audit can tell
// disabled-vs-never-trusted apart).
type TrustedKey struct {
	ID        string
	Key       ed25519.PublicKey
	ExpiresAt time.Time
	Revoked   bool
}

// KeyRing is the canonical trust store. It is safe for concurrent use.
type KeyRing struct {
	mu   sync.RWMutex
	keys map[string]TrustedKey
	now  func() time.Time
}

// NewKeyRing returns a KeyRing seeded with the embedded catalog root key
// under the id "catalog-root". Additional keys can be added with Add.
func NewKeyRing() *KeyRing {
	r := &KeyRing{keys: map[string]TrustedKey{}, now: time.Now}
	r.keys["catalog-root"] = TrustedKey{
		ID:  "catalog-root",
		Key: RootKey(),
	}
	return r
}

// Add inserts or replaces a trusted key.
func (r *KeyRing) Add(k TrustedKey) error {
	if k.ID == "" {
		return errors.New("trust: empty key id")
	}
	if len(k.Key) != ed25519.PublicKeySize {
		return fmt.Errorf("trust: key %q has wrong size %d", k.ID, len(k.Key))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys[k.ID] = k
	return nil
}

// Revoke marks a key as revoked without removing it.
func (r *KeyRing) Revoke(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.keys[id]
	if !ok {
		return fmt.Errorf("trust: unknown key %q", id)
	}
	k.Revoked = true
	r.keys[id] = k
	return nil
}

// Lookup returns the public key for id iff it exists, is not revoked, and
// has not expired. The boolean reports trust; callers should treat false
// as "do not verify anything with this".
func (r *KeyRing) Lookup(id string) (ed25519.PublicKey, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.keys[id]
	if !ok {
		return nil, false
	}
	if k.Revoked {
		return nil, false
	}
	if !k.ExpiresAt.IsZero() && !r.now().Before(k.ExpiresAt) {
		return nil, false
	}
	return k.Key, true
}

// LookupFunc returns a closure compatible with the install package's
// KeyLookup signature so a KeyRing can be injected directly into
// SignatureVerifier.
func (r *KeyRing) LookupFunc() func(string) (ed25519.PublicKey, bool) {
	return r.Lookup
}
