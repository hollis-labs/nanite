package store

import (
	"errors"
	"testing"
)

// Pin lifecycle table-test (C1, CW-20260428-0012). Walks transient → pinned →
// unpinned → re-pinned → cap-exceeded. Transient state lives FE-only, so it's
// represented here as "no row yet"; the backend only sees the pin transition.
func TestBottomDrawerPinnedCard_Lifecycle(t *testing.T) {
	s := newTestStore(t)
	sessionID := "sess-drawer-pins"

	// Step 1: lifecycle starts at zero — no rows.
	got, err := s.ListBottomDrawerPinnedCards(sessionID)
	if err != nil {
		t.Fatalf("ListBottomDrawerPinnedCards: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty pin list, got %d", len(got))
	}

	// Step 2: transient → pinned (insert).
	pin := &BottomDrawerPinnedCard{
		SessionID:  sessionID,
		CardType:   "agent-envelope",
		ContentRef: "envelope-abc",
		Title:      "Search Results",
		Payload:    `{"type":"info-card"}`,
	}
	if err := s.PinBottomDrawerCard(pin); err != nil {
		t.Fatalf("PinBottomDrawerCard: %v", err)
	}
	if pin.ID == "" {
		t.Fatalf("expected pin ID to be generated")
	}
	if pin.CreatedAt == "" {
		t.Fatalf("expected CreatedAt to be stamped")
	}

	got, err = s.ListBottomDrawerPinnedCards(sessionID)
	if err != nil {
		t.Fatalf("ListBottomDrawerPinnedCards (after pin): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(got))
	}
	if got[0].ID != pin.ID {
		t.Fatalf("expected pin ID %q, got %q", pin.ID, got[0].ID)
	}
	if got[0].CardType != "agent-envelope" {
		t.Fatalf("expected card_type 'agent-envelope', got %q", got[0].CardType)
	}

	// Step 3: pinned → unpinned (delete).
	if err := s.UnpinBottomDrawerCard(pin.ID); err != nil {
		t.Fatalf("UnpinBottomDrawerCard: %v", err)
	}
	got, err = s.ListBottomDrawerPinnedCards(sessionID)
	if err != nil {
		t.Fatalf("ListBottomDrawerPinnedCards (after unpin): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 pins after unpin, got %d", len(got))
	}

	// Step 4: unpinned → re-pinned (insert again, fresh row).
	pin2 := &BottomDrawerPinnedCard{
		SessionID: sessionID,
		CardType:  "markdown",
		Title:     "Re-pinned",
	}
	if err := s.PinBottomDrawerCard(pin2); err != nil {
		t.Fatalf("PinBottomDrawerCard (re-pin): %v", err)
	}
	got, err = s.ListBottomDrawerPinnedCards(sessionID)
	if err != nil {
		t.Fatalf("ListBottomDrawerPinnedCards (after re-pin): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 pin after re-pin, got %d", len(got))
	}

	// Cleanup for cap test below.
	_ = s.UnpinBottomDrawerCard(pin2.ID)
}

func TestBottomDrawerPinnedCard_CapEnforced(t *testing.T) {
	s := newTestStore(t)
	sessionID := "sess-cap"

	// Pin up to the cap.
	for i := 0; i < BottomDrawerPinCap; i++ {
		c := &BottomDrawerPinnedCard{
			SessionID: sessionID,
			CardType:  "markdown",
			Title:     "card",
		}
		if err := s.PinBottomDrawerCard(c); err != nil {
			t.Fatalf("pin %d: %v", i, err)
		}
	}

	// Confirm count.
	n, err := s.CountBottomDrawerPinnedCards(sessionID)
	if err != nil {
		t.Fatalf("CountBottomDrawerPinnedCards: %v", err)
	}
	if n != BottomDrawerPinCap {
		t.Fatalf("expected %d pins, got %d", BottomDrawerPinCap, n)
	}

	// 11th pin attempt → ErrBottomDrawerPinCapExceeded.
	overflow := &BottomDrawerPinnedCard{
		SessionID: sessionID,
		CardType:  "markdown",
		Title:     "overflow",
	}
	err = s.PinBottomDrawerCard(overflow)
	if err == nil {
		t.Fatalf("expected ErrBottomDrawerPinCapExceeded, got nil")
	}
	if !errors.Is(err, ErrBottomDrawerPinCapExceeded) {
		t.Fatalf("expected ErrBottomDrawerPinCapExceeded, got %v", err)
	}

	// Unpinning one should let a new pin land.
	all, _ := s.ListBottomDrawerPinnedCards(sessionID)
	if err := s.UnpinBottomDrawerCard(all[0].ID); err != nil {
		t.Fatalf("unpin to free a slot: %v", err)
	}
	if err := s.PinBottomDrawerCard(overflow); err != nil {
		t.Fatalf("pin after unpin slot freed: %v", err)
	}
}

// Per-session isolation: pins on one session don't show up in another's list
// and don't count toward another session's cap.
func TestBottomDrawerPinnedCard_PerSessionIsolation(t *testing.T) {
	s := newTestStore(t)
	sessA := "sess-a"
	sessB := "sess-b"

	for i := 0; i < 5; i++ {
		_ = s.PinBottomDrawerCard(&BottomDrawerPinnedCard{
			SessionID: sessA,
			CardType:  "markdown",
		})
	}

	listA, _ := s.ListBottomDrawerPinnedCards(sessA)
	listB, _ := s.ListBottomDrawerPinnedCards(sessB)
	if len(listA) != 5 {
		t.Fatalf("session A: expected 5 pins, got %d", len(listA))
	}
	if len(listB) != 0 {
		t.Fatalf("session B: expected 0 pins, got %d", len(listB))
	}

	// Session B can pin freely up to its own cap.
	for i := 0; i < BottomDrawerPinCap; i++ {
		if err := s.PinBottomDrawerCard(&BottomDrawerPinnedCard{
			SessionID: sessB,
			CardType:  "markdown",
		}); err != nil {
			t.Fatalf("session B pin %d: %v", i, err)
		}
	}
	cnt, _ := s.CountBottomDrawerPinnedCards(sessB)
	if cnt != BottomDrawerPinCap {
		t.Fatalf("session B: expected cap, got %d", cnt)
	}
}
