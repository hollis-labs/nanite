package store

import "testing"

// F2 (CW-20260429-0002): resolver precedence — per-session override wins over
// user pref when non-nil; nil falls back to user pref.
func TestResolveAutoSwitchEffective_Precedence(t *testing.T) {
	t.Parallel()

	bp := func(b bool) *bool { return &b }

	tests := []struct {
		name           string
		userPref       string
		sessionOverride *bool
		want           AutoSwitchEffective
	}{
		// Override == nil → inherit user pref.
		{"nil override / unset pref", "", nil, AutoSwitchFirstUse},
		{"nil override / always pref", "always", nil, AutoSwitchAuto},
		{"nil override / ask pref", "ask", nil, AutoSwitchAsk},
		{"nil override / never pref", "never", nil, AutoSwitchOff},
		{"nil override / unknown pref", "weird", nil, AutoSwitchAsk},

		// Override == false → always "off", regardless of user pref.
		{"override false beats always", "always", bp(false), AutoSwitchOff},
		{"override false beats ask", "ask", bp(false), AutoSwitchOff},
		{"override false beats never", "never", bp(false), AutoSwitchOff},
		{"override false beats unset", "", bp(false), AutoSwitchOff},

		// Override == true → re-enable, but never bypass first-use, and
		// upgrade "never" to "ask" rather than silently auto-applying.
		{"override true with always pref → auto", "always", bp(true), AutoSwitchAuto},
		{"override true with ask pref → ask", "ask", bp(true), AutoSwitchAsk},
		{"override true with never pref → ask (upgraded)", "never", bp(true), AutoSwitchAsk},
		{"override true with unset pref → firstUse (no bypass)", "", bp(true), AutoSwitchFirstUse},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAutoSwitchEffective(tc.userPref, tc.sessionOverride)
			if got != tc.want {
				t.Errorf("ResolveAutoSwitchEffective(%q, %v) = %q, want %q",
					tc.userPref, tc.sessionOverride, got, tc.want)
			}
		})
	}
}

// F2 (CW-20260429-0002): the per-session override column round-trips through
// CreateSession → SetSessionAutoSwitchOverride → GetSession, and clears
// correctly back to nil.
func TestSetSessionAutoSwitchOverride_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	seedWorkspace(t, s, "ws-f2")

	sess := &Session{WorkspaceID: "ws-f2"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Default — newly created session has no override (nil).
	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.AutoSwitchOverride != nil {
		t.Fatalf("expected nil AutoSwitchOverride on fresh session, got %v", *got.AutoSwitchOverride)
	}

	// Set to true.
	tr := true
	if err := s.SetSessionAutoSwitchOverride(sess.ID, &tr); err != nil {
		t.Fatalf("SetSessionAutoSwitchOverride(true): %v", err)
	}
	got, err = s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after set true: %v", err)
	}
	if got.AutoSwitchOverride == nil || !*got.AutoSwitchOverride {
		t.Fatalf("expected AutoSwitchOverride=true, got %v", got.AutoSwitchOverride)
	}

	// Set to false.
	fa := false
	if err := s.SetSessionAutoSwitchOverride(sess.ID, &fa); err != nil {
		t.Fatalf("SetSessionAutoSwitchOverride(false): %v", err)
	}
	got, err = s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after set false: %v", err)
	}
	if got.AutoSwitchOverride == nil || *got.AutoSwitchOverride {
		t.Fatalf("expected AutoSwitchOverride=false, got %v", got.AutoSwitchOverride)
	}

	// Clear back to nil (inherit user pref).
	if err := s.SetSessionAutoSwitchOverride(sess.ID, nil); err != nil {
		t.Fatalf("SetSessionAutoSwitchOverride(nil): %v", err)
	}
	got, err = s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after clear: %v", err)
	}
	if got.AutoSwitchOverride != nil {
		t.Fatalf("expected nil AutoSwitchOverride after clear, got %v", *got.AutoSwitchOverride)
	}
}

// SetSessionAutoSwitchOverride on a non-existent session must error rather
// than silently no-op.
func TestSetSessionAutoSwitchOverride_MissingSession(t *testing.T) {
	s := newTestStore(t)
	tr := true
	if err := s.SetSessionAutoSwitchOverride("does-not-exist", &tr); err == nil {
		t.Error("expected error setting override on missing session, got nil")
	}
}
