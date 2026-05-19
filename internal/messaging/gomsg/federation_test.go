package gomsg_test

import (
	"context"
	"errors"
	"testing"

	messaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/memstore"
	"github.com/hollis-labs/nanite/internal/messaging/gomsg"
)

func TestRouter_StandaloneIsLocalOnly(t *testing.T) {
	local := memstore.New()
	r := gomsg.NewRouter("nanite.local", local)
	ctx := context.Background()

	to := gomsg.AgentAddress("nanite.local", "s1", "b")
	if _, err := r.Send(ctx, messaging.Envelope{
		Kind: messaging.MsgKindNotice,
		From: gomsg.AgentAddress("nanite.local", "s1", "a"),
		To:   to,
	}); err != nil {
		t.Fatalf("Send local: %v", err)
	}
	got, err := r.Inbox(ctx, to, messaging.Filter{})
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("local inbox: got %d, want 1", len(got))
	}
}

func TestRouter_RoutesByAuthority(t *testing.T) {
	local := memstore.New()
	peer := memstore.New()
	r := gomsg.NewRouter("nanite.local", local)
	if err := r.RegisterPeer("peer.example", peer); err != nil {
		t.Fatalf("RegisterPeer: %v", err)
	}
	ctx := context.Background()

	peerTo := gomsg.AgentAddress("peer.example", "s1", "b")
	if _, err := r.Send(ctx, messaging.Envelope{
		Kind: messaging.MsgKindNotice,
		From: gomsg.AgentAddress("nanite.local", "s1", "a"),
		To:   peerTo,
	}); err != nil {
		t.Fatalf("Send to peer: %v", err)
	}

	// The envelope landed in the peer store, not the local one.
	peerInbox, _ := peer.Inbox(ctx, peerTo, messaging.Filter{})
	if len(peerInbox) != 1 {
		t.Errorf("peer inbox: got %d, want 1", len(peerInbox))
	}
	localInbox, _ := local.Inbox(ctx, peerTo, messaging.Filter{})
	if len(localInbox) != 0 {
		t.Errorf("local inbox leaked peer traffic: got %d", len(localInbox))
	}
}

func TestRouter_UnknownAuthorityUnavailable(t *testing.T) {
	r := gomsg.NewRouter("nanite.local", memstore.New())
	_, err := r.Send(context.Background(), messaging.Envelope{
		Kind: messaging.MsgKindNotice,
		From: gomsg.AgentAddress("nanite.local", "s", "a"),
		To:   gomsg.AgentAddress("ghost.example", "s", "b"),
	})
	if !errors.Is(err, messaging.ErrStoreUnavailable) {
		t.Errorf("got %v, want ErrStoreUnavailable", err)
	}
}

func TestRouter_RegisterPeerGuards(t *testing.T) {
	r := gomsg.NewRouter("nanite.local", memstore.New())
	if err := r.RegisterPeer("nanite.local", memstore.New()); err == nil {
		t.Error("expected error registering the local authority as a peer")
	}
	if err := r.RegisterPeer("", memstore.New()); err == nil {
		t.Error("expected error for empty authority")
	}
	if err := r.RegisterPeer("peer.example", nil); err == nil {
		t.Error("expected error for nil store")
	}
}
