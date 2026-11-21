package engine

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestMailPointerNamesMessage(t *testing.T) {
	body := "Incoming Tether mail. Framing.\n" + `{"id":"m-42","from":"msg://agent/x/y","body":"hello"}`
	if got := mailPointer(body); !strings.Contains(got, "mail m-42 withheld") {
		t.Fatalf("pointer %q", got)
	}
	for _, bad := range []string{"no framing line", "Framing.\n{not json", "Framing.\n" + `{"id":"a\nb"}`, "Framing.\n" + `{"id":"` + strings.Repeat("x", 129) + `"}`} {
		if got := mailPointer(bad); !strings.Contains(got, "read your Tether inbox") {
			t.Fatalf("pointer for %q: %q", bad, got)
		}
	}
}

func TestDeliveredNoticeIDsCountsMailPointer(t *testing.T) {
	pending := []store.Notice{{ID: "mail", Kind: mailNoticeKind}, {ID: "job", Kind: "job_done"}}
	report := slots.Report{slots.Session: {Decisions: []slots.Decision{
		{Owner: "engine", Key: "notice-mail", Disposition: slots.Pointer},
		{Owner: "engine", Key: "notice-job", Disposition: slots.Pointer},
	}}}
	got := deliveredNoticeIDs(report, pending)
	if len(got) != 1 || got[0] != "mail" {
		t.Fatalf("delivered %v", got)
	}
}
