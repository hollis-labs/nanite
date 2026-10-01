package writeclaim

import (
	"strings"
	"testing"
)

const (
	fakeULID = "01M2SFA0ZQ3K4N6P7R8T9V0WXY" // the invented id from the c395 exchange
	realULID = "01M3QSTVA1AMA53J6FTSDEZPN3"
)

func TestIDs(t *testing.T) {
	text := "wrote " + fakeULID + " and CW-20260919-0004, uuid 123e4567-e89b-12d3-a456-426614174000, sha 0123456789abcdef0123456789abcdef01234567, again " + fakeULID
	got := IDs(text)
	want := []string{fakeULID, "CW-20260919-0004", "123e4567-e89b-12d3-a456-426614174000", "0123456789abcdef0123456789abcdef01234567"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("IDs = %v\nwant %v", got, want)
	}
	for _, none := range []string{"", "plain words only", "CW-2026-1", "abcdef", "ordinary12345 text", "TESTING_ALL_UPPERCASE_TOKEN_X"} {
		if ids := IDs(none); len(ids) != 0 {
			t.Errorf("IDs(%q) = %v, want none", none, ids)
		}
	}
}

func TestDetectClaims(t *testing.T) {
	claims := map[string]string{
		"c395 wording (id not in the sentence)": "The Tesseract write succeeded, and the returned item ID above is verified.\n\nID: " + fakeULID,
		"same paragraph, ID first":              "ID " + fakeULID + " — the write succeeded and is verified.",
		"own passive write":                     "Task CW-20260919-0100 was created by me.",
		"wrote":                                 "I wrote memory " + fakeULID + " to Tesseract.",
		"created a tracker id":                  "Created task CW-20260919-0100 in the Nanite project.",
		"saved":                                 "Saved to knowledge as " + fakeULID,
		"pushed a commit":                       "Pushed commit 0123456789abcdef0123456789abcdef01234567 to origin.",
		"create ... successful":                 "Task creation was successful: CW-20260919-0100",
		"inside a longer reply":                 "Here is the summary.\n\nDone. I updated CW-20260919-0100 and recorded " + fakeULID + ".\n\nAnything else?",
		"uuid":                                  "Stored the record as 123e4567-e89b-12d3-a456-426614174000.",
		"modal after the claim (should)":        "I saved it as CW-20260919-0004 and you should see it on the board.",
		"modal after the claim (may)":           "I created CW-20260919-0004; you may want to review it.",
		"modal after the claim (will)":          "I created CW-20260919-0004 which will appear in the list.",
		"modal after the claim (once)":          "I created CW-20260919-0004 once the tool finished.",
		"inline code id":                        "Filed `" + fakeULID + "` as requested.",
	}
	for name, reply := range claims {
		f, ok := Detect(reply, nil)
		if !ok || len(f.IDs) == 0 || f.Phrase == "" || len(f.Ungrounded) != len(f.IDs) {
			t.Errorf("%s: not detected: %+v ok=%v", name, f, ok)
		}
	}
}

func TestDetectIgnoresNonClaims(t *testing.T) {
	quiet := map[string]string{
		"read report, no write verb":    "Task CW-20260919-0011 is in review.",
		"verified alone reports a read": "I confirmed CW-20260919-0011 is in review and verified " + realULID + " exists.",
		"write verb, no id":             "I created a plan and updated the notes.",
		"negated":                       "I could not create CW-20260919-0100; the write failed.",
		"failed":                        "The write failed for " + fakeULID + ".",
		"didn't":                        "I didn't save " + fakeULID + ".",
		"question":                      "Should I create CW-20260919-0100 now?",
		"offer":                         "Would you like me to write " + fakeULID + " to Tesseract?",
		"future":                        "I will create CW-20260919-0100 next.",
		"conditional":                   "If you want, I can save " + fakeULID + " as a memory.",
		"fenced example":                "Example:\n```\ncreated CW-20260919-0100\n```\nThat is the format.",
		"id and verb far apart":         "Created the summary.\n\nSecond block of prose.\n\nThird block.\n\nSee CW-20260919-0100 for details.",
		"empty":                         "",
	}
	for name, reply := range quiet {
		if f, ok := Detect(reply, nil); ok {
			t.Errorf("%s: false positive: %+v", name, f)
		}
	}
}

func TestDetectReportsGroundedIDs(t *testing.T) {
	reply := "Recap: I created " + realULID + " earlier and also wrote " + fakeULID + "."
	f, ok := Detect(reply, map[string]bool{realULID: true})
	if !ok || len(f.IDs) != 2 || len(f.Ungrounded) != 1 || f.Ungrounded[0] != fakeULID {
		t.Fatalf("finding = %+v ok=%v", f, ok)
	}
	f, ok = Detect(reply, map[string]bool{realULID: true, fakeULID: true})
	if !ok || len(f.Ungrounded) != 0 {
		t.Errorf("fully grounded claim = %+v", f)
	}
}

// ---- hardening (detector gaps) ------------------------------------------------

func TestIDsAreCaseInsensitiveAndCanonical(t *testing.T) {
	lower := strings.ToLower(fakeULID)
	if got := IDs("wrote " + lower); len(got) != 1 || got[0] != fakeULID {
		t.Errorf("lowercase ULID = %v, want canonical %s", got, fakeULID)
	}
	if got := IDs("filed cw-20260919-0100"); len(got) != 1 || got[0] != "CW-20260919-0100" {
		t.Errorf("lowercase tracker id = %v", got)
	}
	if got := IDs("uuid 123E4567-E89B-12D3-A456-426614174000"); len(got) != 1 || got[0] != "123e4567-e89b-12d3-a456-426614174000" {
		t.Errorf("uppercase uuid = %v", got)
	}
	if got := IDs("sha 0123456789ABCDEF0123456789ABCDEF01234567"); len(got) != 1 || got[0] != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("uppercase hex = %v", got)
	}
}

// A grounded id is recognized whatever case the reply cites it in.
func TestGroundingIsCaseInsensitive(t *testing.T) {
	f, ok := Detect("I saved it as "+strings.ToLower(realULID)+".", map[string]bool{realULID: true})
	if !ok || len(f.Ungrounded) != 0 {
		t.Errorf("lowercase citation of a grounded id = %+v", f)
	}
}

func TestLookalikeAndFormatCharactersDoNotHideAnID(t *testing.T) {
	cyrillicA := strings.Replace(fakeULID, "A", "А", 1)                    // Cyrillic capital A inside the id
	fullwidth := strings.NewReplacer("0", "０", "1", "１").Replace(fakeULID) // fullwidth digits
	zeroWidth := fakeULID[:10] + "\u200B" + fakeULID[10:]                  // zero-width space in the middle
	softHyphen := fakeULID[:8] + "\u00AD" + fakeULID[8:]
	enDash := "CW–20260919–0100" // en dashes instead of hyphens
	for name, id := range map[string]string{"cyrillic": cyrillicA, "fullwidth": fullwidth, "zero-width": zeroWidth, "soft hyphen": softHyphen} {
		f, ok := Detect("I wrote "+id+" to Tesseract.", nil)
		if !ok || len(f.Ungrounded) != 1 || f.Ungrounded[0] != fakeULID {
			t.Errorf("%s: %+v (want %s)", name, f, fakeULID)
		}
	}
	if f, ok := Detect("Created "+enDash+".", nil); !ok || f.Ungrounded[0] != "CW-20260919-0100" {
		t.Errorf("en-dashed tracker id: %+v", f)
	}
	// A lookalike copy of a grounded id still grounds.
	f, ok := Detect("I recorded "+strings.Replace(realULID, "A", "А", 2)+".", map[string]bool{realULID: true})
	if !ok || len(f.Ungrounded) != 0 {
		t.Errorf("lookalike of a grounded id = %+v", f)
	}
	// Lookalike verbs are folded too ("сreated" with a Cyrillic с).
	if _, ok := Detect("сreated "+fakeULID, nil); !ok {
		t.Error("a Cyrillic lookalike in the verb hid the claim")
	}
}

func TestIDCandidatesNeedIDContent(t *testing.T) {
	for _, s := range []string{
		"the word ABCDEFGHJKMNPQRSTVWXYZABCD is 26 letters long with no digits",
		"called 1234567890123456 on the phone", // 16 digits, no letter
		"wrote thisisnotahexdigestatalllllll",  // long word
		"saved order 20260919 number 12345678", // numbers
	} {
		if ids := IDs(s); len(ids) != 0 {
			t.Errorf("IDs(%q) = %v, want none", s, ids)
		}
	}
}

// Every claiming paragraph counts, not just the first: a grounded first claim
// must not hide a fabricated second one.
func TestDetectAggregatesAllClaims(t *testing.T) {
	reply := "I saved " + realULID + " earlier.\n\nAlso, I wrote " + fakeULID + " just now."
	f, ok := Detect(reply, map[string]bool{realULID: true})
	if !ok || len(f.IDs) != 2 || len(f.Ungrounded) != 1 || f.Ungrounded[0] != fakeULID {
		t.Fatalf("finding = %+v", f)
	}
}

// An id in a neighboring paragraph pairs with a claim only when the claim
// names an id; otherwise unrelated ids and unrelated claims stay unrelated.
func TestAdjacentParagraphNeedsAnIDReference(t *testing.T) {
	for _, reply := range []string{
		"Created the summary.\n\nSee CW-20260919-0100 for details.",
		"See CW-20260919-0100 for details.\n\nUpdated the notes.",
	} {
		if f, ok := Detect(reply, nil); ok {
			t.Errorf("unrelated neighbors paired: %+v\n%s", f, reply)
		}
	}
	if _, ok := Detect("The write succeeded and the returned ID is below.\n\n"+fakeULID, nil); !ok {
		t.Error("a claim that names its id must take the neighboring paragraph's id")
	}
	// Its own paragraph always wins over a neighbor.
	f, ok := Detect("Unrelated "+realULID+".\n\nI created CW-20260919-0100 just now.", nil)
	if !ok || len(f.IDs) != 1 || f.IDs[0] != "CW-20260919-0100" {
		t.Errorf("own-paragraph id not preferred: %+v", f)
	}
}

// Modals after the claim in the same sentence must not disarm it (review of
// the first guard); modals before it still do.
func TestModalAfterTheClaimDoesNotEvade(t *testing.T) {
	for _, reply := range []string{
		"I saved it as CW-20260919-0100 and you should see it on the board.",
		"I created CW-20260919-0100; you may want to review it.",
		"I created CW-20260919-0100 which will appear in the list.",
		"I created CW-20260919-0100 once the tool finished.",
	} {
		if _, ok := Detect(reply, nil); !ok {
			t.Errorf("evaded: %s", reply)
		}
	}
	for _, reply := range []string{
		"I will create CW-20260919-0100 next.",
		"You should create CW-20260919-0100 first.",
		"I could not save CW-20260919-0100.",
	} {
		if f, ok := Detect(reply, nil); ok {
			t.Errorf("false positive: %s -> %+v", reply, f)
		}
	}
}

// IDs is used on its own for tool output (what a write returned), so it folds
// lookalikes itself rather than relying on Detect to have done it.
func TestIDsFoldLookalikesOnTheirOwn(t *testing.T) {
	for name, text := range map[string]string{
		"cyrillic":   `{"id":"` + strings.Replace(realULID, "A", "\u0410", 1) + `"}`,
		"zero-width": realULID[:9] + "\u200B" + realULID[9:],
		"fullwidth":  strings.ReplaceAll(realULID, "0", "\uFF10"),
	} {
		if got := IDs(text); len(got) != 1 || got[0] != realULID {
			t.Errorf("%s: IDs = %v, want [%s]", name, got, realULID)
		}
	}
}

// A grounded set built in any case still grounds the canonical id.
func TestGroundedSetKeysNeedNotBeCanonical(t *testing.T) {
	f, ok := Detect("I saved "+realULID+".", map[string]bool{strings.ToLower(realULID): true})
	if !ok || len(f.Ungrounded) != 0 {
		t.Errorf("lowercase grounded key did not ground: %+v", f)
	}
}

func TestActivityReportsAreNotAssistantWriteClaims(t *testing.T) {
	for _, reply := range []string{
		"Observed: many tasks were created or updated on October 1 across PRJ-20260416-0001 and PRJ-20260417-0002.",
		"Task CW-20260919-0011 was updated yesterday.",
		"Torque reports that CW-20260919-0011 was created yesterday.",
		"I found that CW-20260919-0011 was updated yesterday.",
		"I reviewed records created under PRJ-20260416-0001.",
	} {
		if f, ok := Detect(reply, nil); ok {
			t.Errorf("activity report rejected: %q: %+v", reply, f)
		}
	}
}

func TestWriteReceiptDoesNotClaimOtherSentencesRecords(t *testing.T) {
	reply := "The activity spans PRJ-20260416-0001 and PRJ-20260417-0002. I saved the summary as " + realULID + ". Task CW-20260919-0011 was updated yesterday."
	f, ok := Detect(reply, map[string]bool{realULID: true})
	if !ok || len(f.IDs) != 1 || f.IDs[0] != realULID || len(f.Ungrounded) != 0 {
		t.Fatalf("read citations were treated as write receipts: %+v, detected=%v", f, ok)
	}
	// A valid receipt must not excuse a separate fabricated write claim.
	f, ok = Detect(reply+" I also created "+fakeULID+".", map[string]bool{realULID: true})
	if !ok || len(f.Ungrounded) != 1 || f.Ungrounded[0] != fakeULID {
		t.Fatalf("fabricated write escaped the guard: %+v, detected=%v", f, ok)
	}
}
