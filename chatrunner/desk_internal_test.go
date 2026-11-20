package chatrunner

import (
	"testing"

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
		got, job := d.jobFor(tc.n)
		if (got != nil) != tc.want || (tc.want && job != "01JOB") {
			t.Errorf("%s %q: got %v %q", tc.n.Kind, tc.n.Body, got != nil, job)
		}
	}
}
