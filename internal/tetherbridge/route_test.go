package tetherbridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// fakeStore drives the three outcomes. The ambiguous case is the reason this
// is a fake rather than a real store: nothing in the schema currently lets two
// live instances of one profile bind the same session, so the guard could not
// be exercised against a real database without first creating the state the
// guard exists to reject. Testing it here means the guard is proven now rather
// than the first time the product allows it.
type fakeStore struct {
	inst      *store.DurableAgentInstance
	instErr   error
	slotCount int
	slotErr   error

	slotAskedProfile string
	slotAskedSession string
}

func (f *fakeStore) GetDurableAgentInstanceByURN(_ context.Context, _ string) (*store.DurableAgentInstance, error) {
	return f.inst, f.instErr
}

func (f *fakeStore) CountInstancesSharingMailboxSlot(_ context.Context, profileID, sessionID string) (int, error) {
	f.slotAskedProfile, f.slotAskedSession = profileID, sessionID
	return f.slotCount, f.slotErr
}

func TestResolveActorRoute_BoundActorResolvesToTheMailboxSlot(t *testing.T) {
	f := &fakeStore{
		inst: &store.DurableAgentInstance{
			ID: "inst-1", ProfileID: "prof-1", CurrentSessionID: "sess-1",
		},
		slotCount: 1,
	}
	got, err := ResolveActorRoute(context.Background(), f, "msg://agent/nanite/agt_aaaaaaaaaa")
	if err != nil {
		t.Fatalf("ResolveActorRoute: %v", err)
	}
	if !got.Bound() {
		t.Error("route should be Bound when the instance has a current session")
	}
	if got.SessionID != "sess-1" || got.ProfileID != "prof-1" {
		t.Errorf("slot = (%s, %s), want (sess-1, prof-1)", got.SessionID, got.ProfileID)
	}
	if got.InstanceID != "inst-1" {
		t.Errorf("InstanceID = %q, want inst-1 — the actor is carried even though it is not the address", got.InstanceID)
	}
	// The guard must be asked about the resolved slot, not about anything else.
	if f.slotAskedProfile != "prof-1" || f.slotAskedSession != "sess-1" {
		t.Errorf("slot guard asked about (%s, %s), want (prof-1, sess-1)", f.slotAskedProfile, f.slotAskedSession)
	}
}

// TestResolveActorRoute_UnboundActorIsNotAnError is the majority case: 16 of 18
// live instances have no current session. An unbound actor means the mail stays
// in Tether, which is why this must not be an error and must not be reported as
// a bound route with an empty session.
func TestResolveActorRoute_UnboundActorIsNotAnError(t *testing.T) {
	f := &fakeStore{
		inst: &store.DurableAgentInstance{ID: "inst-2", ProfileID: "prof-2", CurrentSessionID: ""},
	}
	got, err := ResolveActorRoute(context.Background(), f, "msg://agent/nanite/agt_bbbbbbbbbb")
	if err != nil {
		t.Fatalf("unbound actor returned an error: %v", err)
	}
	if got.Bound() {
		t.Error("Bound() true for an actor with no current session")
	}
	if got.InstanceID != "inst-2" || got.ProfileID != "prof-2" {
		t.Errorf("unbound route lost the actor: %+v", got)
	}
	// Asking the slot guard about an empty session would be meaningless; the
	// resolver must short-circuit instead.
	if f.slotAskedSession != "" {
		t.Errorf("slot guard was consulted for an unbound actor (session %q)", f.slotAskedSession)
	}
}

// TestResolveActorRoute_AmbiguousSlotIsRefused is the condition the package
// header names. Two live instances of one profile in one session make
// (session, profile) stop identifying a single recipient, and the architecture
// forbids resolving that by picking one.
func TestResolveActorRoute_AmbiguousSlotIsRefused(t *testing.T) {
	f := &fakeStore{
		inst: &store.DurableAgentInstance{
			ID: "inst-3", ProfileID: "prof-shared", CurrentSessionID: "sess-shared",
		},
		slotCount: 2,
	}
	_, err := ResolveActorRoute(context.Background(), f, "msg://agent/nanite/agt_cccccccccc")
	if err == nil {
		t.Fatal("a slot shared by two live instances was accepted; mail from two actors " +
			"would interleave in one mailbox slot with nothing downstream able to detect it")
	}
	if !errors.Is(err, ErrMailboxSlotAmbiguous) {
		t.Errorf("error = %v, want ErrMailboxSlotAmbiguous", err)
	}
	// The message has to carry enough to act on without a debugger.
	for _, want := range []string{"inst-3", "sess-shared", "prof-shared"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message omits %q: %v", want, err)
		}
	}
}

func TestResolveActorRoute_UnknownActorIsDistinctFromUnbound(t *testing.T) {
	f := &fakeStore{instErr: store.ErrDurableAgentInstanceNotFound}
	_, err := ResolveActorRoute(context.Background(), f, "msg://agent/nanite/agt_dddddddddd")
	if !errors.Is(err, ErrActorUnknown) {
		t.Fatalf("error = %v, want ErrActorUnknown", err)
	}
	// Distinctness matters: an actor Nanite does not know is not an actor with
	// nowhere to deliver. Conflating them would have Nanite claim recipients
	// that belong to something else.
	if errors.Is(err, ErrMailboxSlotAmbiguous) {
		t.Error("unknown actor reported as an ambiguous slot")
	}
}

func TestResolveActorRoute_NilStore(t *testing.T) {
	if _, err := ResolveActorRoute(context.Background(), nil, "msg://agent/nanite/agt_eeeeeeeeee"); err == nil {
		t.Error("nil store accepted")
	}
}
