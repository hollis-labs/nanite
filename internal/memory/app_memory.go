package memory

import (
	"context"
	"encoding/hex"
	"strings"
)

// AppNamespace selects Nanite-authored app memory, rather than claiming a verified human
// principal. Tesseract's fixed-depth app grammar has one shared namespace;
// immutable, injectively encoded user keys enforce visibility independently
// of mutable tags. HTTP's current single-user host always selects default.
func AppNamespace(memoryType ...string) string {
	return AppMemoryPrefix() + "/" + resolveMemoryType(memoryType)
}
func AppMemoryPrefix() string { return "app/nanite/memory" }
func appUserKeyPrefix(user string) string {
	if user == "" {
		user = "default"
	}
	return "u_" + hex.EncodeToString([]byte(user)) + "."
}
func AppMemoryKey(user, key string) string { return appUserKeyPrefix(user) + key }
func AppMemoryAccessible(user, namespace, key string) bool {
	return !strings.HasPrefix(namespace, AppMemoryPrefix()+"/") || strings.HasPrefix(key, appUserKeyPrefix(user))
}
func readsAppMemory(opts RecallOpts) bool {
	if len(opts.Namespaces) == 0 {
		return true
	}
	for _, ns := range opts.Namespaces {
		selector := strings.TrimSuffix(ns, "/*")
		if selector == "app" || selector == "app/nanite" || selector == AppMemoryPrefix() || strings.HasPrefix(selector, AppMemoryPrefix()+"/") {
			return true
		}
	}
	return false
}

// StoreAppForUser is an explicit host assembler port. Request metadata and
// generic Store never select the app actor. The caller supplies an already
// scoped key, so the same logical key cannot overwrite another user's item.
func (s *Service) StoreAppForUser(ctx context.Context, user string, m Memory) error {
	bound := &Service{store: s.store, appUser: user}
	return bound.write(ctx, m, true)
}
func (s *Service) RecallAppForUser(ctx context.Context, user string, opts RecallOpts) ([]Memory, error) {
	bound := &Service{store: s.store, appUser: user}
	return bound.Recall(ctx, opts)
}
