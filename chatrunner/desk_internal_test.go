package chatrunner

import (
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/desk"

	"github.com/hollis-labs/nanite/agent"
)

// Only a subtask job's own finish notice, naming its exact ID, maps to the
// job: not the subtask's question, and not a longer ID that contains it.
func TestJobForMatchesOnlyAJobFinish(t *testing.T) {
	run := &planRun{id: "p"}
	d := &Desk{jobs: map[string]*planRun{"01JOB": run}}
	for _, tc := range []struct {
		n    agent.NoticePosted
		want bool
	}{
		{agent.NoticePosted{Kind: subtaskJobKind, Body: "subtask 01JOB (research) ended ok; usage 1 input. Output: /x/01JOB.log"}, true},
		{agent.NoticePosted{Kind: "subtask_ask", Body: `Subtask 01JOB (research, "x") is waiting for an answer`}, false},
		{agent.NoticePosted{Kind: subtaskJobKind, Body: "subtask 01JOBX (research) ended ok"}, false},
		{agent.NoticePosted{Kind: "shell", Body: "shell job 01JOB succeeded."}, false},
	} {
		got, job, _ := d.jobFor(tc.n)
		if (got != nil) != tc.want || (tc.want && job != "01JOB") {
			t.Errorf("%s %q: got %v %q", tc.n.Kind, tc.n.Body, got != nil, job)
		}
	}
}

// A notice for a job whose finish was applied already is recognized, so it
// is dropped rather than delivered as a plain notice.
func TestJobForKnowsRetiredJobs(t *testing.T) {
	d := &Desk{jobs: map[string]*planRun{}, retired: map[string]bool{"01JOB": true}}
	run, job, retired := d.jobFor(agent.NoticePosted{Kind: subtaskJobKind, Body: "subagent job 01JOB was interrupted by a process exit"})
	if run != nil || job != "01JOB" || !retired {
		t.Fatalf("got %v %q %v", run != nil, job, retired)
	}
}

// Suggested plans nobody answered expire after a day, and a chat keeps at
// most maxSuggestions, dropping the oldest.
func TestSuggestionsExpireAndAreCapped(t *testing.T) {
	now := time.Now()
	c := &deskChat{suggested: map[string]desk.Plan{}, suggestedAt: map[string]time.Time{}}
	c.suggest("stale", desk.Plan{}, now.Add(-25*time.Hour))
	for i := range maxSuggestions + 2 {
		c.suggest(fmt.Sprintf("p%02d", i), desk.Plan{}, now.Add(time.Duration(i)*time.Second))
	}
	if len(c.suggested) != maxSuggestions || len(c.suggestedAt) != maxSuggestions {
		t.Fatalf("kept %d (%d times)", len(c.suggested), len(c.suggestedAt))
	}
	for _, gone := range []string{"stale", "p00", "p01"} {
		if _, ok := c.suggested[gone]; ok {
			t.Fatalf("%s was kept", gone)
		}
	}
}

// The desk-wide set of retired jobs forgets what a chat's list drops.
func TestRetiredJobsArePruned(t *testing.T) {
	d := &Desk{retired: map[string]bool{}}
	c := &deskChat{}
	for i := range maxRetiredJobs + 5 {
		d.retireLocked(c, fmt.Sprintf("job%d", i))
	}
	if len(c.retired) != maxRetiredJobs || len(d.retired) != maxRetiredJobs || d.retired["job0"] || !d.retired[fmt.Sprintf("job%d", maxRetiredJobs+4)] {
		t.Fatalf("chat %d, desk %d", len(c.retired), len(d.retired))
	}
}
