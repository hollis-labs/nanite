package service

import "github.com/hollis-labs/nanite/internal/store"

// NativeAuthorityAdmin carries the one writer of native-view authority. It is
// wired in the container and used only by the operator-authenticated API
// handlers; TestLocalAuthorityWriterHasOneConsumer pins that.
type NativeAuthorityAdmin struct {
	Writer *store.LocalAuthority
}
