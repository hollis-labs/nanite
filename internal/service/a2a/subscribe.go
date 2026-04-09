// Package a2a — subscribe.go
//
// This file contains a stub pubsub used by the Service's SendMessage path.
// Task 7 replaces it with the real in-process pubsub implementation that
// powers MCP streaming subscribers.
package a2a

import "github.com/hollis-labs/nanite/internal/store"

// pubsub is a stub placeholder. Its publish method is a no-op so that
// Service.SendMessage can unconditionally invoke it today without depending
// on the (future) subscriber machinery.
type pubsub struct{}

func newPubsub() *pubsub                        { return &pubsub{} }
func (p *pubsub) publish(_ *store.A2AMessage)   {}
