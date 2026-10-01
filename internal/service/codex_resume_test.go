package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/structuredmessage"
)

// go-providers v0.41.0 and agentkit v0.20.4 (CW-20260930-0113, the latest-libs
// bump): a Codex exec session resumes its thread from turn 2 instead of
// starting a new thread every turn. Turn 1's thread.started reports the thread
// id, and turn 2 runs `exec ... resume <id> -- <prompt>`.

// fakeCodexResumeScript stands in for codex-cli 0.159.2 in exec mode. It keeps
// one file per thread, and it enforces codex's own argument grammar, which the
// libs' fakes do not: after `resume <id>` only `--` and the prompt may follow,
// so an exec flag spliced there (the `--cd` after `resume` bug that
// go-providers v0.41.0 needs agentkit v0.20.4 to avoid) is refused exactly as
// codex refuses it: "error: unexpected argument '--cd' found", exit 2. A turn
// that does not resume starts a new thread, which has no memory of the last.
// It answers "what is the code word" from its thread's own history. %[1]s is a
// scratch dir.
const fakeCodexResumeScript = `#!/bin/sh
dir="%[1]s"
# Real codex keeps a thread's rollout under $CODEX_HOME, and Nanite points that
# at the per-boot dir, so a thread does not outlive its boot.
tdir="${CODEX_HOME:-$dir}/threads"
printf '%%s ' "$@" | tr '\n' ' ' >> "$dir/argv.log"
echo >> "$dir/argv.log"
state=pre
id=
for a in "$@"; do
  case "$state" in
    pre) [ "$a" = "resume" ] && state=want_id ;;
    want_id) id="$a"; state=post_id ;;
    post_id)
      if [ "$a" = "--" ]; then state=prompt
      else echo "error: unexpected argument '$a' found" >&2; exit 2; fi ;;
  esac
done
prompt="$a"
mkdir -p "$tdir"
if [ -n "$id" ]; then
  if [ ! -f "$tdir/$id" ]; then echo "no rollout found for thread id $id" >&2; exit 1; fi
  thread="$id"
else
  n=$(cat "$dir/threads.count" 2>/dev/null || echo 0)
  n=$((n+1))
  echo "$n" > "$dir/threads.count"
  thread="thr-0113-$n"
  : > "$tdir/$thread"
fi
printf '%%s\n' "$prompt" >> "$tdir/$thread"
case "$prompt" in
  *"what is the code word"*)
    word=$(sed -n 's/.*the code word is \([a-z]*\).*/\1/p' "$tdir/$thread" | head -1)
    reply="${word:-I do not know}" ;;
  *) reply="noted" ;;
esac
echo "{\"type\":\"thread.started\",\"thread_id\":\"$thread\"}"
echo '{"type":"turn.started"}'
echo "{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"$reply\"}}"
echo '{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}'
`

// A two-turn Codex session keeps its context: turn 1 states a fact, turn 2
// asks for it back and gets it, because turn 2 resumed turn 1's thread.
func TestCodexExecSecondTurnResumesTheThreadAndKeepsContext(t *testing.T) {
	dir := t.TempDir()
	tc := codexSubprocessCase
	tc.script = fmt.Sprintf(fakeCodexResumeScript, dir)
	f := newNativeCLIFixture(t, tc)
	owner := lifecycle.NewManager("test.codex-resume")
	t.Cleanup(func() { _ = owner.Shutdown(5 * time.Second) })
	f.svc.lifecycle = owner
	f.svc.activeGen = make(map[string]*inFlightGen)
	ctx := context.Background()

	ask := func(content string) string {
		t.Helper()
		msgID, err := f.svc.HandleMessage(ctx, f.session, content)
		if err != nil {
			t.Fatalf("HandleMessage(%q): %v", content, err)
		}
		events := drainTurnStream(t, subscribe(t, f, msgID))
		if findEvent(events, "stream_end") == nil {
			t.Fatalf("%q: no stream_end; events %+v", content, events)
		}
		if errEvent := findEvent(events, "error"); errEvent != nil {
			t.Fatalf("%q: error event %+v", content, errEvent)
		}
		saved, err := f.st.GetMessage(ctx, msgID)
		if err != nil || saved == nil {
			t.Fatalf("%q: reply not saved: %v", content, err)
		}
		text, _ := structuredmessage.UnwrapText(saved.Content)
		return text
	}

	if got := ask("Remember this: the code word is plum."); got != "noted" {
		t.Fatalf("turn 1 reply = %q, want %q", got, "noted")
	}
	if got := ask("Now, what is the code word?"); got != "plum" {
		t.Fatalf("turn 2 reply = %q, want %q: turn 2 did not resume turn 1's thread", got, "plum")
	}

	calls := strings.Split(strings.TrimRight(readFileString(t, filepath.Join(dir, "argv.log")), "\n"), "\n")
	if len(calls) != 2 {
		t.Fatalf("codex ran %d times, want one process per turn: %q", len(calls), calls)
	}
	if strings.Contains(calls[0], " resume ") {
		t.Fatalf("turn 1 resumed a thread it did not have: %q", calls[0])
	}
	// Turn 2's argv: every exec option in front of `resume <id> --`, then the
	// prompt, which does not itself carry the fact (so the fake's answer can
	// only have come from the thread).
	i := strings.Index(calls[1], " resume thr-0113-1 -- ")
	if i < 0 {
		t.Fatalf("turn 2 did not run `resume thr-0113-1 -- <prompt>`: %q", calls[1])
	}
	if strings.Contains(calls[1][i:], "plum") {
		t.Fatalf("turn 2's prompt carries the fact itself, so the test proves nothing: %q", calls[1])
	}
	if got := strings.TrimSpace(readFileString(t, filepath.Join(dir, "threads.count"))); got != "1" {
		t.Fatalf("codex started %s threads, want 1 (turn 2 must resume, not start a new one)", got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a file the fake CLI wrote in this test's own t.TempDir()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// A Codex thread does not outlive its boot: real codex keeps a thread's
// rollout under $CODEX_HOME, which Nanite points at the per-boot dir. So a
// cold boot (host restart, an evicted or killed session) must start a fresh
// thread, not `exec resume <old id>`, which codex refuses with "no rollout
// found for thread id" and the turn would fail. Before go-providers v0.41.0
// Codex reported no thread id, so this always worked; with it, Nanite has to
// keep the id from reaching a later boot (providerSessionSurvivesBoot).
func TestCodexColdBootStartsAFreshThreadInsteadOfResumingOne(t *testing.T) {
	dir := t.TempDir()
	tc := codexSubprocessCase
	tc.script = fmt.Sprintf(fakeCodexResumeScript, dir)
	f := newNativeCLIFixture(t, tc)
	owner := lifecycle.NewManager("test.codex-coldboot")
	t.Cleanup(func() { _ = owner.Shutdown(5 * time.Second) })
	f.svc.lifecycle = owner
	f.svc.activeGen = make(map[string]*inFlightGen)
	ctx := context.Background()

	ask := func(content string) []chat.StreamEvent {
		t.Helper()
		msgID, err := f.svc.HandleMessage(ctx, f.session, content)
		if err != nil {
			t.Fatalf("HandleMessage(%q): %v", content, err)
		}
		return drainTurnStream(t, subscribe(t, f, msgID))
	}
	if events := ask("Remember this: the code word is plum."); findEvent(events, "stream_end") == nil || findEvent(events, "error") != nil {
		t.Fatalf("turn 1 did not end cleanly: %v", eventTypes(events))
	}

	// The runtime goes away between the turns, as on a host restart.
	sess, ok := f.svc.agentDeps.Manager.Load(f.session)
	if !ok {
		t.Fatal("no runtime session after turn 1")
	}
	_ = sess.Stop(ctx)

	events := ask("Now, what is the code word?")
	if e := findEvent(events, "error"); e != nil {
		t.Fatalf("the first turn after a cold boot failed: %+v (a stored codex thread id was resumed in a boot that does not have the thread)", e)
	}
	if findEvent(events, "stream_end") == nil {
		t.Fatalf("turn 2 did not end: %v", eventTypes(events))
	}

	calls := strings.Split(strings.TrimRight(readFileString(t, filepath.Join(dir, "argv.log")), "\n"), "\n")
	if len(calls) != 2 {
		t.Fatalf("codex ran %d times, want one process per turn: %q", len(calls), calls)
	}
	if strings.Contains(calls[1], " resume ") {
		t.Fatalf("turn 2 resumed a thread across a boot: %q", calls[1])
	}
	if got := strings.TrimSpace(readFileString(t, filepath.Join(dir, "threads.count"))); got != "2" {
		t.Fatalf("codex started %s threads, want 2: one per boot", got)
	}
	// Nothing was persisted for a later boot to resume.
	var stored string
	if err := f.st.DB.QueryRowContext(ctx, `SELECT COALESCE(provider_session_id, '') FROM agent_runtime WHERE id = ?`, f.session).Scan(&stored); err != nil {
		t.Fatalf("read agent_runtime row: %v", err)
	}
	if stored != "" {
		t.Fatalf("agent_runtime.provider_session_id = %q, want none for a codex thread no later boot can resume", stored)
	}
}
