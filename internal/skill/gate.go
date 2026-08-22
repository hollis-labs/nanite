package skill

// gate.go — TASKS/skills/09
// (TASKS/skills/09-sandbox-and-capability-policy-gate-for-skill-execution.md):
// docs/engineering/architecture/20-skills.md's "Security, sandboxing, and
// trust" section, with one real correction found during this batch's
// planning (see that task file's own Context section, and
// TASKS/ESCALATIONS.md's 2026-08-21 entry, for the full record): there is
// no live wrapper.Config.Policy/policy.Engine pipeline running in Nanite
// today for this task to "plug into," so this file builds narrow, direct
// capability enforcement at its own call site instead — gated on task 02's
// agent_known_skills grant-state columns (ApprovedContentHash/GrantedAt/
// GrantedBy/CapabilitiesGranted) — matching what
// TASKS/plugin-system/06 (capability-enforcement-at-rpc-proxy-layer)
// independently decided for the same reason.
//
// This is exec.go's (task 08, already landed) GatedExecutor implementation
// — the *only* place in this package (and, per this task's own Done-means,
// in the whole batch of skill-script/materializer execution paths) that is
// allowed to call sandbox.Apply. exec.go's own ExecuteScript/
// ResolveInlineMarkers never construct a subprocess themselves; they build
// an ExecRequest and hand it to whatever GatedExecutor is injected. Gate is
// that implementation.
//
// # Capability vocabulary (What-to-do item 1)
//
// Capabilities is the typed shape this task defines for
// agent_known_skills.capabilities_granted (task 02's column — deliberately
// left loose/JSON for this task to specify). Every field maps directly
// onto a real, enforceable go-sandbox@v0.2.1 sandbox.Profile field; nothing
// here is invented beyond what sandbox.Apply can actually back, per this
// task's own explicit instruction:
//
//   - FS combines the architecture doc's illustrative "fs-read" and
//     "fs-write" resource kinds into one structure, because
//     sandbox.Profile itself expresses filesystem access as one
//     FSSpec{Read,Write,Deny} — splitting them into two independent JSON
//     keys would just require this file to immediately recombine them
//     before composing a Profile, with no enforcement benefit.
//   - Network maps onto Profile.Net/AllowLoopback.
//   - SubprocessSpawn maps onto Profile.Subprocess.
//   - "environment-secret-access" (the fifth resource kind
//     20-skills.md's illustrative list names) is deliberately absent as a
//     *grantable* capability: sandbox.Profile has no field expressing
//     environment-variable scoping at all (confirmed by reading
//     go-sandbox@v0.2.1's Profile struct directly, sandbox/profile.go) —
//     there is nothing for a grant of this kind to actually back, and this
//     task's own instruction is explicit not to invent one.
//
//     This does NOT mean the sandboxed command's environment is inherited
//     unmodified, though. A fresh review after this task's initial landing
//     (TASKS/skills/09's "Fix required" section) found that it previously
//     was — Gate.run never set cmd.Env at all, so exec.Cmd's own "nil Env
//     means inherit" default leaked the full, unfiltered host process
//     environment (including whatever real secrets the running Nanite
//     server process holds) into every sandboxed skill execution
//     unconditionally, regardless of capability grant. Gate.run now sets
//     cmd.Env to a secret-filtered copy of the inherited environment
//     (filterSecretEnv, below) before calling sandbox.Apply, as a floor
//     applied to every execution — not something a capability grant can
//     opt out of, and not itself part of the Capabilities vocabulary above.
//     This mirrors internal/sandbox/exec.go's own filterSecrets/isSecretKey
//     pattern (already used there for the identical problem class —
//     UserExec's own "env := filterSecrets(os.Environ())"), kept here as
//     this package's own local copy (filterSecretEnv/isSecretEnvKey) rather
//     than a cross-package export of that package's unexported helpers —
//     matching this file's own established precedent elsewhere
//     (composeProfile vs. buildSandboxProfile, DecisionMode vs. policy.Mode)
//     of adapting a pattern locally rather than always importing across
//     packages whose two call sites' needs might diverge over time.
//
// # A real, load-bearing finding about go-sandbox@v0.2.1's cross-platform
// enforcement strength — investigated directly against the vendored
// library's source (sandbox/apply_darwin.go, sandbox/apply_linux.go,
// sandbox/doc.go), not assumed
//
// The two backends enforce a Profile very differently, and this file's
// design (and its own test suite) is written with both realities in mind
// rather than assuming either one:
//
//   - macOS (sandbox-exec/SBPL): doc.go states the posture plainly —
//     "default-allow with selective denies." BuildSBPL always emits
//     "(allow default)"; Profile.FS.Write and Profile.FS.Read entries
//     either duplicate that default-allow (Write) or are validated but
//     otherwise inert (Read — apply_darwin.go's own comment: "Read paths
//     are no-ops under default-allow"). The *only* filesystem-access
//     restriction macOS's backend can actually express is Profile.FS.Deny
//     (denies both file-read* and file-write* together for the listed
//     paths). Concretely: on this platform, a capability grant with an
//     *empty* FS.Write list does NOT, by itself, block a write to some
//     other, unlisted path — that write succeeds via the default-allow
//     posture regardless. Net (network) and Subprocess (fork/exec) are,
//     by contrast, both genuinely, robustly enforced on macOS via real
//     SBPL deny rules.
//   - Linux (bubblewrap): the opposite posture — genuinely default-deny.
//     buildBwrapArgs starts from a nearly-empty mount namespace (a narrow,
//     hardcoded set of read-only interpreter/TLS paths plus workspace),
//     and only Profile.FS.Write (--bind, writable) and Profile.FS.Read
//     (--ro-bind-try, real read-only) entries are made visible at all —
//     anything else genuinely does not exist inside the sandboxed process'
//     view of the filesystem. Profile.FS.Deny, however, is a silent no-op
//     on this backend: buildBwrapArgs never references it at all. Net is
//     enforced via a real network-namespace unshare; Subprocess is
//     explicitly documented as *not* enforced at all on Linux
//     (apply_linux.go's own comment on Apply).
//
// Net effect: there is no single Profile field that is both meaningful and
// robustly enforced on *both* platforms for "block an unlisted write."
// FS.Deny is the mechanism that actually restricts filesystem access on
// macOS; plain omission from FS.Write/FS.Read is what actually restricts
// it on Linux (where FS.Deny itself does nothing). This file's
// composeProfile therefore forwards a grant's FS.Deny entries unconditionally
// on every platform (harmless on Linux, load-bearing on macOS), and this
// task's own test suite (gate_test.go) deliberately grants an explicit
// FS.Deny entry for the "outside the granted bounds" path its Done-means
// test targets, rather than relying on bare omission — see that file's own
// comments for why bare omission alone is not, on this platform, a
// sufficient proof of real enforcement. This is a genuine, pre-existing
// limitation inherited from the vendored go-sandbox@v0.2.1 library (its own
// doc.go: "Tightening to default-deny requires a well-tested per-OS
// allowlist and is intentionally left to a future sprint") — fixing the
// library's own macOS backend is out of this task's scope; this file works
// correctly and honestly within the library's real, current behavior.
//
// # SubprocessSpawn's default (true, not gated by default)
//
// docs/engineering/architecture/20-skills.md's own default-posture
// description ("FS.Write empty..., Net: false, unless capabilities_granted
// explicitly authorizes more") names only FS.Write and Net — it does not
// list Subprocess among the fields a grant must explicitly widen. This is
// deliberate, not an oversight this file is silently working around:
// nearly every realistic marker or script — including the Agent-Skills-
// spec's own canonical example, `` !`git diff HEAD` `` — has to fork/exec a
// real external program to do anything useful at all under "compute."
// Defaulting Subprocess to false would make the "default posture" this
// task builds unable to run that canonical example without an elevated
// grant, which would contradict "read/compute/materialize" being a
// genuinely usable default. This is also safe: sandbox enforcement (both
// backends) applies to the whole sandboxed process tree, not just the
// initial binary, so a forked child remains just as constrained by FS/Net
// as its parent — allowing Subprocess by default does not create a way to
// bypass either gate. SubprocessSpawn is still a real, grantable field
// (*bool, nil = default-true) for the rare skill that must never fork/exec
// at all.
//
// # The Apply "workspace" parameter is unconditionally writable — composeProfile
// never passes a meaningful directory as it
//
// Both backends' Apply implementations treat their third parameter
// (workspace) as always writable regardless of Profile contents — macOS's
// own comment: "Workspace is always writable, regardless of FS.Write
// contents." Passing req.WorkDir (a skill's own vendored package
// directory, for a script) as workspace would therefore make it writable
// no matter what a grant says, violating internal/skillvendor.Store.Path's
// own "treat the returned directory as read-only" contract. This file
// instead always creates a fresh, disposable, per-execution scratch
// directory purely to satisfy Apply's structural requirement for *some*
// workspace value, and never tells the executed command about it — the
// command's real working directory (cmd.Dir) stays req.WorkDir, and
// req.WorkDir is separately added to the profile's FS.Read (never
// FS.Write) so the command can find and read its own files without ever
// making that directory writable through this mechanism.
//
// # No-grant-row resolution (What-to-do item 4)
//
// docs/engineering/architecture/20-skills.md's "Security, sandboxing, and
// trust" section frames the default posture entirely in terms of *approved*
// execution ("Approval is granted against a specific hash..."); nothing in
// that section (or anywhere else in the doc) describes an ungranted
// skill/agent pair executing under an ambient default posture. Read
// directly, "default posture is read/compute/materialize" describes the
// *capability breadth* once a grant exists and doesn't itself elevate
// anything further — it is not a statement that no grant is needed at all.
// This file therefore refuses execution outright — no ambient capability —
// whenever no agent_known_skills row exists for (AgentID, SkillSlug) at
// all, *or* a row exists but has never been through an explicit approval
// step (empty ApprovedContentHash — e.g. a bare row AssignSkillToAgent left
// behind, store.AgentKnownSkill.IsBareAssignment's exact shape). This
// reading was confirmed directly against 20-skills.md's own text before
// implementing, not assumed from the task file's own paraphrase — see this
// task's Work Log for the full record. This is a design-latitude
// resolution of an instruction the task file itself flagged as
// worth double-checking, not a stop-and-escalate case: nothing in the doc
// actually points the other way.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/go-sandbox/sandbox"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentKnownSkillStore is the narrow slice of *store.Store's API Gate
// depends on for looking up an agent's grant row. A real *store.Store
// satisfies this directly; matches this package's existing
// AgentContextResolverStore/SkillIndexStore DI-for-testability convention
// (resolver.go) — there is only ever one real implementation in
// production.
type AgentKnownSkillStore interface {
	GetAgentKnownSkill(ctx context.Context, agentID, skillName string) (*store.AgentKnownSkill, error)
}

// FSCapability mirrors sandbox.FSSpec directly (Read/Write/Deny path
// lists) — see this file's package doc for why "fs-read" and "fs-write"
// are combined into one structure here rather than split into two
// independent top-level capability keys.
type FSCapability struct {
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// NetworkCapability maps onto sandbox.Profile.Net/AllowLoopback.
type NetworkCapability struct {
	Allow         bool `json:"allow,omitempty"`
	AllowLoopback bool `json:"allow_loopback,omitempty"`
}

// Capabilities is the typed shape this task defines for
// agent_known_skills.capabilities_granted — see this file's package doc
// for the full reasoning behind exactly these fields and no others.
type Capabilities struct {
	FS      *FSCapability      `json:"fs,omitempty"`
	Network *NetworkCapability `json:"network,omitempty"`
	// SubprocessSpawn maps onto sandbox.Profile.Subprocess. nil means
	// "use the default" (true — see package doc); an explicit false
	// revokes fork/exec entirely for a skill that must never spawn
	// anything.
	SubprocessSpawn *bool `json:"subprocess_spawn,omitempty"`
}

// ParseCapabilities decodes a agent_known_skills.capabilities_granted
// value. An empty string (the common case: a grant with no elevated
// capabilities beyond the default posture) returns the zero Capabilities,
// not an error.
func ParseCapabilities(raw string) (Capabilities, error) {
	if raw == "" {
		return Capabilities{}, nil
	}
	var c Capabilities
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Capabilities{}, fmt.Errorf("skill gate: parse capabilities_granted: %w", err)
	}
	return c, nil
}

// DecisionMode names the gate's decision action for one execution request.
// Deliberately mirrors go-agent-wrapper@v0.8.1's policy.Mode taxonomy
// (observe/nudge/rewrite/block/approval) for the future-compatibility
// reason this task's own Context section names — if that host-level
// policy.Engine/policy.Store mechanism ever does get wired live in Nanite,
// this package's decisions already speak a compatible vocabulary. This is
// this package's own type, not an import of the currently-unwired
// go-agent-wrapper/policy package — no code is shared, because there is no
// live code to share yet.
type DecisionMode string

const (
	DecisionObserve  DecisionMode = "observe"
	DecisionNudge    DecisionMode = "nudge"
	DecisionRewrite  DecisionMode = "rewrite"
	DecisionBlock    DecisionMode = "block"
	DecisionApproval DecisionMode = "approval"
)

// GateDecision is what Gate's internal authorization step produces for one
// ExecuteGated call, before any actual sandboxed execution happens. Logged
// unconditionally (see logDecision) — this is the "log/return the decision"
// half of this task's own instruction; ExecuteGated's return signature is
// fixed by exec.go's already-landed GatedExecutor interface
// (ExecuteGated(ctx, req) (ExecResult, error)), so a decision is never
// threaded back to the caller as a distinct return value — it is logged,
// and (for a refusal) also carried inside the returned error's own typed
// fields (GrantRequiredError / ReapprovalRequiredError).
type GateDecision struct {
	Mode      DecisionMode
	SkillSlug string
	AgentID   string
	Message   string
}

// GrantRequiredError is returned when no valid, approved capability grant
// exists for (AgentID, SkillSlug) — either no agent_known_skills row
// exists at all, or a row exists but has never been through an explicit
// approval step (empty ApprovedContentHash — e.g. a bare row
// AssignSkillToAgent left behind, store.AgentKnownSkill.IsBareAssignment's
// exact shape). See this file's package doc, "No-grant-row resolution,"
// for why both cases are treated identically: neither is an ambient
// capability.
type GrantRequiredError struct {
	SkillSlug string
	AgentID   string
	// Reason is a short, human-readable explanation distinguishing the two
	// cases above: "no grant row exists" or "grant row exists but has
	// never been approved".
	Reason string
}

func (e *GrantRequiredError) Error() string {
	return fmt.Sprintf("skill %q: agent %q has no valid capability grant (%s) — execution refused", e.SkillSlug, e.AgentID, e.Reason)
}

// ReapprovalRequiredError is returned when a grant exists and was
// previously approved, but the skill's current vendored content hash no
// longer matches the hash the grant was approved against — the vendored
// source changed (a new install/sync) since approval.
// docs/engineering/architecture/20-skills.md's "Security, sandboxing, and
// trust" section: "a changed source requires a new explicit install before
// it affects anything, and that new install carries a new hash requiring
// its own approval." This is never a silent fallback to the old approval.
type ReapprovalRequiredError struct {
	SkillSlug    string
	AgentID      string
	ApprovedHash string
	CurrentHash  string
}

func (e *ReapprovalRequiredError) Error() string {
	return fmt.Sprintf(
		"skill %q: agent %q's approval (content hash %s) no longer matches the skill's current vendored content (hash %s) — re-approval required before this skill can execute again",
		e.SkillSlug, e.AgentID, e.ApprovedHash, e.CurrentHash,
	)
}

// Gate implements exec.go's GatedExecutor interface — TASKS/skills/09.
// Construct via NewGate. The zero value is not usable (Skills/Grants are
// both required).
type Gate struct {
	// Skills looks up a skill's current catalog row (for its current
	// vendored ContentHash). Reuses resolver.go's existing
	// SkillIndexStore interface — a real *store.Store satisfies both.
	Skills SkillIndexStore
	// Grants looks up an agent's grant row for a given skill.
	Grants AgentKnownSkillStore
}

// NewGate constructs a Gate. Both arguments are required — a Gate with
// either set to nil returns a configuration error from ExecuteGated rather
// than panicking.
func NewGate(skills SkillIndexStore, grants AgentKnownSkillStore) *Gate {
	return &Gate{Skills: skills, Grants: grants}
}

// compile-time interface assertion: Gate satisfies exec.go's GatedExecutor.
var _ GatedExecutor = (*Gate)(nil)

// ExecuteGated implements GatedExecutor. It is the sole place in this
// package (and, per this task's own Done-means, in this whole batch's
// skill-script/materializer execution paths) that ever calls
// sandbox.Apply.
func (g *Gate) ExecuteGated(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if g.Skills == nil || g.Grants == nil {
		return ExecResult{}, fmt.Errorf("skill gate: not configured (Skills and Grants stores are both required)")
	}
	if req.SkillSlug == "" || req.AgentID == "" {
		return ExecResult{}, fmt.Errorf("skill gate: SkillSlug and AgentID are both required")
	}
	if len(req.Command) == 0 {
		return ExecResult{}, fmt.Errorf("skill gate: %s %q: empty command", req.Kind, req.Label)
	}

	caps, err := g.Authorize(ctx, req.SkillSlug, req.AgentID)
	if err != nil {
		return ExecResult{}, err
	}

	return g.run(ctx, req, caps)
}

// Authorize confirms agentID has a valid, currently-approved capability
// grant for skillSlug, returning the grant's parsed Capabilities on
// success — the same trust-validity decision ExecuteGated makes before
// ever executing anything, exposed directly (TASKS/skills/11) so a caller
// that needs to confirm access WITHOUT executing a command can reuse the
// exact same check and the exact same typed errors (GrantRequiredError /
// ReapprovalRequiredError) instead of re-deriving the decision from
// scratch. TASKS/skills/11's skill_get self-tool is the motivating
// caller: a skill with no “ !`cmd` “ markers or scripts/ entries (plain
// instructional content, the common case) never reaches ExecuteGated any
// other way, since ResolveInlineMarkers is a no-op on a body with no
// markers — without this exposed method, an ungranted or stale-approval
// agent could fetch such a skill's full content with zero enforcement.
// ExecuteGated itself is unchanged behaviorally: it now calls this method
// internally instead of the unexported authorize() directly, so there
// remains exactly one place this decision is made.
func (g *Gate) Authorize(ctx context.Context, skillSlug, agentID string) (Capabilities, error) {
	if g.Skills == nil || g.Grants == nil {
		return Capabilities{}, fmt.Errorf("skill gate: not configured (Skills and Grants stores are both required)")
	}
	if skillSlug == "" || agentID == "" {
		return Capabilities{}, fmt.Errorf("skill gate: SkillSlug and AgentID are both required")
	}
	caps, decision, err := g.authorize(ctx, ExecRequest{SkillSlug: skillSlug, AgentID: agentID})
	logDecision(decision, err)
	return caps, err
}

// authorize is the decision half of this task's own "gate's entry point"
// instruction (What-to-do item 3/4): look up the skill's current vendored
// content hash and the agent's grant row, confirm the grant is real and
// current, and — only if it is — parse its granted capabilities. Never
// touches the filesystem or spawns anything itself.
func (g *Gate) authorize(ctx context.Context, req ExecRequest) (Capabilities, GateDecision, error) {
	sk, err := g.Skills.GetSkillBySlug(ctx, req.SkillSlug)
	if err != nil {
		msg := fmt.Sprintf("skill catalog lookup for %q failed", req.SkillSlug)
		return Capabilities{}, blockDecision(req, msg), fmt.Errorf("skill gate: look up skill %q: %w", req.SkillSlug, err)
	}
	if sk == nil {
		msg := fmt.Sprintf("skill %q is not in the skill catalog", req.SkillSlug)
		return Capabilities{}, blockDecision(req, msg), errors.New("skill gate: " + msg)
	}

	grant, err := g.Grants.GetAgentKnownSkill(ctx, req.AgentID, req.SkillSlug)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnownSkillNotFound) {
			gerr := &GrantRequiredError{SkillSlug: req.SkillSlug, AgentID: req.AgentID, Reason: "no grant row exists"}
			return Capabilities{}, blockDecision(req, gerr.Error()), gerr
		}
		msg := fmt.Sprintf("grant lookup for agent %q / skill %q failed", req.AgentID, req.SkillSlug)
		return Capabilities{}, blockDecision(req, msg), fmt.Errorf("skill gate: look up grant: %w", err)
	}

	if grant.ApprovedContentHash == "" {
		gerr := &GrantRequiredError{SkillSlug: req.SkillSlug, AgentID: req.AgentID, Reason: "grant row exists but has never been approved"}
		return Capabilities{}, GateDecision{Mode: DecisionApproval, SkillSlug: req.SkillSlug, AgentID: req.AgentID, Message: gerr.Error()}, gerr
	}

	if grant.ApprovedContentHash != sk.ContentHash {
		rerr := &ReapprovalRequiredError{
			SkillSlug: req.SkillSlug, AgentID: req.AgentID,
			ApprovedHash: grant.ApprovedContentHash, CurrentHash: sk.ContentHash,
		}
		return Capabilities{}, GateDecision{Mode: DecisionApproval, SkillSlug: req.SkillSlug, AgentID: req.AgentID, Message: rerr.Error()}, rerr
	}

	caps, err := ParseCapabilities(grant.CapabilitiesGranted)
	if err != nil {
		msg := fmt.Sprintf("capabilities_granted for agent %q / skill %q is malformed", req.AgentID, req.SkillSlug)
		return Capabilities{}, blockDecision(req, msg), fmt.Errorf("skill gate: %s: %w", msg, err)
	}

	return caps, GateDecision{Mode: DecisionObserve, SkillSlug: req.SkillSlug, AgentID: req.AgentID, Message: "granted"}, nil
}

func blockDecision(req ExecRequest, msg string) GateDecision {
	return GateDecision{Mode: DecisionBlock, SkillSlug: req.SkillSlug, AgentID: req.AgentID, Message: msg}
}

// logDecision is the "log... the decision" half of this task's own
// instruction (What-to-do item 5). Uses this codebase's standard
// package-level slog convention (internal/service/container.go and many
// others) rather than a custom logger threaded through Gate.
func logDecision(d GateDecision, err error) {
	if err != nil {
		slog.Warn("skill gate decision", "mode", d.Mode, "skill", d.SkillSlug, "agent", d.AgentID, "message", d.Message, "err", err)
		return
	}
	slog.Info("skill gate decision", "mode", d.Mode, "skill", d.SkillSlug, "agent", d.AgentID)
}

// run composes a sandbox.Profile from caps and executes req.Command under
// it via a real sandbox.Apply call — the one call site this whole package
// (and this batch) is allowed to have, per this task's own Done-means.
func (g *Gate) run(ctx context.Context, req ExecRequest, caps Capabilities) (ExecResult, error) {
	profile := composeProfile(caps, req.WorkDir, req.SkillSlug)

	// Apply's workspace parameter is unconditionally writable on every
	// backend regardless of Profile contents (see package doc) — always a
	// fresh, disposable, per-execution scratch directory, never
	// req.WorkDir, so a grant with no fs-write capability never
	// accidentally makes the skill's own (read-only-by-contract) vendored
	// directory writable through this side channel.
	scratchDir, err := os.MkdirTemp("", "nanite-skill-exec-*")
	if err != nil {
		return ExecResult{}, fmt.Errorf("skill gate: create exec scratch workspace: %w", err)
	}
	defer os.RemoveAll(scratchDir)
	if resolved, rerr := filepath.EvalSymlinks(scratchDir); rerr == nil {
		scratchDir = resolved
	}

	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...)
	cmd.Dir = req.WorkDir

	// Secret-filtered environment — a floor applied unconditionally to
	// every sandboxed execution, independent of any capability grant. Set
	// before sandbox.Apply so the profile is applied to a command that
	// already carries the correct (filtered) environment; confirmed by
	// reading both apply_darwin.go and apply_linux.go directly that neither
	// backend resets or otherwise clobbers a caller-supplied cmd.Env
	// (apply_darwin.go never references cmd.Env at all; apply_linux.go's
	// inheritedEnv only falls back to a fresh os.Environ() read when
	// cmd.Env is nil, so a non-nil, already-filtered cmd.Env here is
	// preserved as-is on both platforms). See this file's package doc for
	// the bug this fixes (TASKS/skills/09's "Fix required" section).
	cmd.Env = filterSecretEnv(os.Environ())

	cleanup, err := sandbox.Apply(cmd, profile, scratchDir)
	if err != nil {
		return ExecResult{}, fmt.Errorf("skill gate: apply sandbox profile: %w", err)
	}
	defer cleanup()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if runErr != nil {
		return res, fmt.Errorf("skill gate: sandboxed execution failed: %w", runErr)
	}
	return res, nil
}

// composeProfile builds the sandbox.Profile a grant's Capabilities
// authorize. workDir (req.WorkDir) is always added to FS.Read — never
// FS.Write — regardless of caps, so the executed command can find and
// read its own files without that requiring (or implying) any write
// capability grant. See this file's package doc for the full reasoning
// behind every field below.
func composeProfile(caps Capabilities, workDir, skillSlug string) sandbox.Profile {
	p := sandbox.Profile{ID: "skill-exec:" + skillSlug}

	if caps.FS != nil {
		p.FS.Read = append([]string(nil), caps.FS.Read...)
		p.FS.Write = append([]string(nil), caps.FS.Write...)
		p.FS.Deny = append([]string(nil), caps.FS.Deny...)
	}
	if workDir != "" {
		p.FS.Read = appendUniquePath(p.FS.Read, workDir)
	}

	if caps.Network != nil {
		p.Net = caps.Network.Allow
		p.AllowLoopback = caps.Network.AllowLoopback
	}

	p.Subprocess = true
	if caps.SubprocessSpawn != nil {
		p.Subprocess = *caps.SubprocessSpawn
	}

	return p
}

func appendUniquePath(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// secretEnvKeyPatterns are substrings that identify environment variable
// names likely to hold secrets. Matching is case-insensitive. Mirrors
// internal/sandbox/exec.go's own secretKeyPatterns list exactly (kept as
// this package's own local copy rather than a cross-package export of that
// package's unexported helpers — see this file's package doc, "environment-
// secret-access," for the full reasoning).
var secretEnvKeyPatterns = []string{
	"KEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"CREDENTIAL",
	"AUTH",
}

// filterSecretEnv returns a copy of environ (in "KEY=value" form, exactly
// os.Environ()'s own shape) with every entry whose key matches
// secretEnvKeyPatterns removed. Applied unconditionally by Gate.run to
// every sandboxed skill execution, regardless of capability grant — this is
// a floor, not something a grant can opt out of. Fixes the bug found in
// fresh review (TASKS/skills/09's "Fix required" section): Gate.run
// previously never set cmd.Env at all, so exec.Cmd's own "nil Env means
// inherit" default leaked the full, unfiltered host process environment —
// including real secrets — into every sandboxed skill's ExecResult.Stdout
// via an ordinary "compute" marker such as “ !`env` “, with no elevated
// capability grant required.
func filterSecretEnv(environ []string) []string {
	filtered := make([]string, 0, len(environ))
	for _, entry := range environ {
		eqIdx := strings.IndexByte(entry, '=')
		if eqIdx < 0 {
			continue
		}
		if !isSecretEnvKey(entry[:eqIdx]) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// isSecretEnvKey reports whether name matches any secretEnvKeyPatterns
// substring, case-insensitively.
func isSecretEnvKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, pattern := range secretEnvKeyPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}
