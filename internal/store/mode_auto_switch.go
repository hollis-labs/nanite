package store

// mode_auto_switch.go — F2 (CW-20260429-0002).
//
// Pure resolver for the effective mode-auto-switch behavior given a user-level
// preference and an optional per-session override. The actual auto-apply
// happens in the FE (ChatTranscript reads SSE mode_suggestion and decides
// whether to apply); this resolver mirrors the FE precedence so backend
// callers (or future server-side automation) stay in sync.
//
// Precedence:
//   1. If sessionOverride is non-nil, it wins:
//        - true  → "auto" when userPref allows ("always") else "ask"; if userPref
//                  is unset (""), still "firstUse" — the override does not
//                  bypass the first-use prompt.
//        - false → "off" (suppress all auto-switches).
//   2. Otherwise fall through to the user pref:
//        - ""       → "firstUse"
//        - "always" → "auto"
//        - "ask"    → "ask"
//        - "never"  → "off"
//
// Returned states:
//   - "auto"     apply suggestion + show toast
//   - "ask"      render compact 2-option confirm card
//   - "off"      discard the suggestion silently
//   - "firstUse" render the 5-option first-use prompt
//
// Any unknown user pref falls back to "ask" (defensive default).

// AutoSwitchEffective is the resolved per-session auto-switch behavior.
type AutoSwitchEffective string

const (
	AutoSwitchAuto     AutoSwitchEffective = "auto"
	AutoSwitchAsk      AutoSwitchEffective = "ask"
	AutoSwitchOff      AutoSwitchEffective = "off"
	AutoSwitchFirstUse AutoSwitchEffective = "firstUse"
)

// ResolveAutoSwitchEffective returns the effective auto-switch behavior for a
// session given the user's global preference and the per-session override.
//
//   - sessionOverride == nil      → inherit user pref
//   - sessionOverride == &false   → "off"
//   - sessionOverride == &true    → "auto" (only when user pref already permits
//     auto-switching), "firstUse" when user pref is unset, otherwise "ask"
//
// See the file-level comment for the full table.
func ResolveAutoSwitchEffective(userPref string, sessionOverride *bool) AutoSwitchEffective {
	if sessionOverride != nil && !*sessionOverride {
		return AutoSwitchOff
	}

	// Compute the inherited base state from the user pref.
	var base AutoSwitchEffective
	switch userPref {
	case "":
		base = AutoSwitchFirstUse
	case "always":
		base = AutoSwitchAuto
	case "never":
		base = AutoSwitchOff
	case "ask":
		base = AutoSwitchAsk
	default:
		base = AutoSwitchAsk
	}

	if sessionOverride != nil && *sessionOverride {
		// "On" override — re-enable auto-switching for this session, but never
		// bypass first-use. If the user has explicitly chosen "never", an
		// override of true upgrades it to "ask" (least-surprise: the user has
		// asked the agent to consider switches for this session, but we still
		// confirm rather than silently apply).
		if base == AutoSwitchFirstUse {
			return AutoSwitchFirstUse
		}
		if base == AutoSwitchOff {
			return AutoSwitchAsk
		}
		return base
	}

	return base
}
