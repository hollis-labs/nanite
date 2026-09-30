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
		"wrote":                                 "I wrote memory " + fakeULID + " to Tesseract.",
		"created a tracker id":                  "Created task CW-20260919-0100 in the Nanite project.",
		"saved":                                 "Saved to knowledge as " + fakeULID,
		"pushed a commit":                       "Pushed commit 0123456789abcdef0123456789abcdef01234567 to origin.",
		"create ... successful":                 "Task creation was successful: CW-20260919-0100",
		"inside a longer reply":                 "Here is the summary.\n\nDone. I updated CW-20260919-0100 and recorded " + fakeULID + ".\n\nAnything else?",
		"uuid":                                  "Stored the record as 123e4567-e89b-12d3-a456-426614174000.",
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
