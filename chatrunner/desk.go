package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/desk"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
)

// ActivityDeskPlan reports the front desk's plan for a turn's message.
// Value: id, disposition ("suggest" or "dispatch"), topic, card (plain
// text) and plan (desk.Plan JSON). A suggested plan runs once the host
// calls Desk.AcceptPlan.
const ActivityDeskPlan = "nanite.desk.plan"

// NoticeKindDesk is the kind of the notices the front desk publishes: one
// composed release of the delivery editor.
const NoticeKindDesk = "desk"

// DeskOptions turns on the front desk (libs agent/desk) for a runner: an
// intake planner reads each user message, plans are dispatched as
// background subtasks and tracked in a topic ledger, and every notice that
// no running turn carried (background results, relayed asks, host
// notices) goes through a delivery editor that paces them into composed
// messages. Dispatching needs Options.Observe and the agent's background
// tools.
type DeskOptions struct {
	// Intake configures the planner. Mode off (the zero value) plans
	// nothing; the delivery editor still paces notices.
	Intake desk.Intake
	// PlanWithModel, when Intake.Planner is nil, plans with a desk.Model
	// on the agent's own provider: the providers.yaml use "desk"
	// (agent.UseDesk), falling back to chat. PlannerInstructions are added
	// to its prompt (the host's seats and variants, say).
	PlanWithModel       bool
	PlannerInstructions string
	// IntakeTimeout bounds planning before a turn starts (default 15s).
	// When it runs out the message goes to the chat agent unplanned.
	IntakeTimeout time.Duration
	// Policy is the delivery policy (default desk.DefaultPolicy()).
	Policy *desk.Policy
	// Ledger keeps the topics; nil starts an empty one. The host persists
	// it (it marshals to JSON).
	Ledger *desk.Ledger
	// Variants maps a work kind to the subtask variant that runs it.
	// Defaults: research and analysis → research, council and delegate →
	// worker.
	Variants map[desk.WorkKind]string
	// Delegate hands a delegate item to the seat that owns it (mail, a
	// task) and returns the ref the host will report its outcome under with
	// Desk.Complete. Nil runs delegate items as worker subtasks.
	Delegate func(ctx context.Context, chatID string, topic desk.TopicID, item desk.PlanItem) (desk.Ref, error)
	// Mode returns a chat's deliberate mode, for the planner and the
	// delivery policy.
	Mode func(chatID string) string
	// OnPlan sees every intake result, shadow ones included.
	OnPlan func(chatID string, r desk.Result)
	// OnCorrection sees the owner's answer to each suggested plan.
	OnCorrection func(chatID string, c desk.Correction)
}

// Desk is a runner's front desk.
type Desk struct {
	r      *Runner
	opts   DeskOptions
	ledger *desk.Ledger
	policy desk.Policy
	logger *slog.Logger

	// advancing serializes advances, so two never race each other for
	// the conversation.
	advancing sync.Mutex

	mu      sync.Mutex
	chats   map[string]*deskChat // by chat conversation ID
	jobs    map[string]*planRun  // background job ID → its plan
	retired map[string]bool      // job IDs whose finish was applied
	workers map[string]*noticeWorker
	seq     int

	// ledgerGen counts changes to the ledger (under mu).
	ledgerGen int

	// saveMu serializes saves (see persist); the fields after it are
	// under it. ownLedger is set when the desk made the ledger, and so
	// saves and restores it; ledgerFrozen when the saved ledger is from a
	// newer version and must not be overwritten.
	saveMu         sync.Mutex
	ownLedger      bool
	ledgerFrozen   bool
	savedLedgerGen int
}

// noticeWorker handles one Nanite conversation's notices in order, so a
// slow host call (Delegate, a dispatch) holds up only that conversation.
type noticeWorker struct {
	mu    sync.Mutex
	queue []agent.NoticePosted
	done  bool
	wake  chan struct{}
}

// maxDispatchAttempts is how many times an item's dispatch may fail
// (other than for a held conversation) before it is marked failed.
const maxDispatchAttempts = 3

type deskChat struct {
	editor      *desk.Editor
	timer       *time.Timer
	typing      bool
	suggested   map[string]desk.Plan
	suggestedAt map[string]time.Time
	plans       []*planRun
	retired     []string // recent job IDs whose finish was applied, oldest first
	saved       string   // the state last saved, under saveMu
	// frozen is set when the chat's saved state is from a newer version:
	// it is neither read nor overwritten.
	frozen bool
}

// planRun is a plan being carried out on one chat.
type planRun struct {
	id       string
	chatID   string
	topic    desk.TopicID
	plan     desk.Plan
	state    map[string]desk.WorkState // item ID → state
	result   map[string]string         // item ID → result text
	attempts map[string]int            // item ID → failed dispatch attempts
	asked    map[string]string         // item ID → question ID, while open
	refs     map[desk.Ref]string       // delegated ref → item ID
}

func (p *planRun) workID(item string) string { return p.id + "/" + item }

func (p *planRun) done() bool {
	for _, it := range p.plan.Items {
		if !p.state[it.ID].Finished() {
			return false
		}
	}
	return true
}

// ready is the items whose dependencies have all finished and that have
// not started.
func (p *planRun) ready() []desk.PlanItem {
	var out []desk.PlanItem
	for _, it := range p.plan.Items {
		if p.state[it.ID] != desk.WorkPlanned {
			continue
		}
		ok := true
		for _, d := range it.DependsOn {
			if !p.state[d].Finished() {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

func newDesk(r *Runner, opts DeskOptions) (*Desk, error) {
	policy := desk.DefaultPolicy()
	if opts.Policy != nil {
		policy = *opts.Policy
	}
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("chatrunner: desk policy: %w", err)
	}
	if opts.IntakeTimeout <= 0 {
		opts.IntakeTimeout = 15 * time.Second
	}
	if opts.Intake.Planner == nil && opts.PlanWithModel {
		opts.Intake.Planner = desk.Model{Instructions: opts.PlannerInstructions,
			Complete: func(ctx context.Context, system, user string) (string, error) {
				return r.agent.Complete(ctx, agent.UseDesk, system, user, 2048)
			}}
	}
	ledger := opts.Ledger
	if ledger == nil {
		ledger = desk.NewLedger(nil)
	}
	logger := r.opts.Agent.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Desk{r: r, opts: opts, ledger: ledger, ownLedger: opts.Ledger == nil, policy: policy, logger: logger, chats: map[string]*deskChat{},
		jobs: map[string]*planRun{}, retired: map[string]bool{}, workers: map[string]*noticeWorker{}}, nil
}

// Ledger returns the desk's topic ledger.
func (d *Desk) Ledger() *desk.Ledger { return d.ledger }

func (d *Desk) chat(chatID string) *deskChat {
	c := d.chats[chatID]
	if c == nil {
		ed, _ := desk.NewEditor(d.policy, desk.WithLedger(d.ledger)) // the policy was validated
		c = &deskChat{editor: ed, suggested: map[string]desk.Plan{}, suggestedAt: map[string]time.Time{}}
		d.chats[chatID] = c
	}
	return c
}

func (d *Desk) mode(chatID string) string {
	if d.opts.Mode == nil {
		return ""
	}
	return d.opts.Mode(chatID)
}

func (d *Desk) variant(kind desk.WorkKind) string {
	if v := d.opts.Variants[kind]; v != "" {
		return v
	}
	if kind == desk.KindResearch || kind == desk.KindAnalysis {
		return "research"
	}
	return "worker"
}

func (d *Desk) nextID(prefix string) string {
	d.seq++
	return fmt.Sprintf("%s%d-%d", prefix, time.Now().UnixMilli(), d.seq)
}

// Held counts a chat's queued items by delivery action.
func (d *Desk) Held(chatID string) map[desk.Action]int {
	d.mu.Lock()
	c := d.chats[chatID]
	d.mu.Unlock()
	if c == nil {
		return map[desk.Action]int{}
	}
	return c.editor.Held()
}

// SetOwnerTyping tells the desk the owner is typing in a chat (or has
// stopped), so it holds everything but critical items and questions.
func (d *Desk) SetOwnerTyping(chatID string, typing bool) {
	d.mu.Lock()
	d.chat(chatID).typing = typing
	d.mu.Unlock()
	if !typing {
		d.pump(chatID)
	}
}

// Flush publishes everything a chat's editor holds as one message.
func (d *Desk) Flush(chatID string) bool {
	d.mu.Lock()
	c := d.chats[chatID]
	d.mu.Unlock()
	if c == nil {
		return false
	}
	m, ok := c.editor.Flush()
	if ok {
		d.persist(chatID) // released before it goes out, as in pump
		d.publish(chatID, m)
	}
	return ok
}

// beforeTurn runs intake on a user message. It answers the desk's open
// questions on the chat with the message, plans it, and returns a note for
// the chat agent, activity for the start of the turn, and the plan to
// dispatch before the turn (nil for none).
func (d *Desk) beforeTurn(ctx context.Context, chatID, text string) (note string, prelude []chatstream.Event, run *planRun) {
	d.answerOpen(chatID, text)
	mode := d.mode(chatID)
	if d.opts.Intake.Mode == "" || d.opts.Intake.Mode == desk.IntakeOff || strings.TrimSpace(text) == "" {
		return "", nil, nil
	}
	planCtx, cancel := context.WithTimeout(ctx, d.opts.IntakeTimeout)
	res, err := d.opts.Intake.Read(planCtx, desk.Request{Message: text, Mode: mode, Topics: d.ledger.OpenTopics()})
	cancel()
	if err != nil {
		d.logger.Warn("chatrunner: desk intake", "conversation", chatID, "error", err.Error())
		return "", nil, nil
	}
	if res.Err != "" {
		d.logger.Warn("chatrunner: desk planner failed; used the rules' plan", "conversation", chatID, "error", res.Err)
	}
	if d.opts.OnPlan != nil {
		d.opts.OnPlan(chatID, res)
	}
	switch res.Disposition {
	case desk.Suggest:
		d.mu.Lock()
		id := d.nextID("plan-")
		d.chat(chatID).suggest(id, res.Plan, time.Now())
		d.mu.Unlock()
		d.persist(chatID)
		card := res.Plan.Card()
		return "The front desk suggested this plan for the message. It has not started; the owner can accept it on its card. Answer the message as usual.\n" + card,
			[]chatstream.Event{planActivity(id, desk.Suggest, res.Plan.Topic, res.Plan)}, nil
	case desk.Dispatch:
		run, err := d.newRun(chatID, res.Plan)
		if err != nil {
			d.logger.Warn("chatrunner: desk plan", "conversation", chatID, "error", err.Error())
			return "", nil, nil
		}
		return "The front desk started this plan in the background (topic " + string(run.topic) + "). Its results reach the owner when they are ready; do not redo that work. Answer what is left for you.\n" + res.Plan.Card(),
			[]chatstream.Event{planActivity(run.id, desk.Dispatch, run.topic, res.Plan)}, run
	case desk.Chat:
	}
	return "", nil, nil
}

func planActivity(id string, disposition desk.Disposition, topic desk.TopicID, p desk.Plan) chatstream.Event {
	return activity(ActivityDeskPlan, map[string]any{"id": id, "disposition": disposition, "topic": topic, "card": p.Card(), "plan": p})
}

// newRun opens (or continues) the plan's topic and records its items as
// planned work.
func (d *Desk) newRun(chatID string, p desk.Plan) (*planRun, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	topic := p.Topic
	if topic != "" {
		if t, err := d.ledger.Get(topic); err != nil || t.Status != desk.TopicOpen {
			topic = ""
		}
	}
	if topic == "" {
		title := p.TopicTitle
		if title == "" {
			title = firstLine(p.Message, 60)
		}
		t, err := d.ledger.Open("", title, p.Mode, desk.Ref{Kind: "chat", ID: chatID})
		if err != nil {
			return nil, err
		}
		d.touchLedger()
		topic = t.ID
	}
	d.mu.Lock()
	run := &planRun{id: d.nextID("plan-"), chatID: chatID, topic: topic, plan: p, state: map[string]desk.WorkState{},
		result: map[string]string{}, asked: map[string]string{}, refs: map[desk.Ref]string{}, attempts: map[string]int{}}
	d.mu.Unlock()
	for _, it := range p.Items {
		run.state[it.ID] = desk.WorkPlanned
		if err := d.ledger.Attach(topic, desk.Work{ID: run.workID(it.ID), Kind: it.Kind, Summary: it.Goal}); err != nil {
			return nil, err
		}
	}
	d.touchLedger()
	d.mu.Lock()
	d.chat(chatID).plans = append(d.chat(chatID).plans, run)
	d.mu.Unlock()
	d.persist(chatID)
	return run, nil
}

// AcceptPlan runs a suggested plan, as suggested or as the owner edited
// it. Its first steps start now, or as soon as a running turn ends.
func (d *Desk) AcceptPlan(ctx context.Context, chatID, planID string, edited *desk.Plan) error {
	d.mu.Lock()
	proposed, ok := d.chat(chatID).suggested[planID]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("chatrunner: no suggested plan %q", planID)
	}
	final := proposed
	if edited != nil {
		final = *edited
		final.Message, final.Source = proposed.Message, proposed.Source
	}
	if err := final.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	if _, still := d.chat(chatID).suggested[planID]; !still {
		d.mu.Unlock()
		return fmt.Errorf("chatrunner: no suggested plan %q", planID)
	}
	d.chat(chatID).unsuggest(planID)
	d.mu.Unlock()
	d.persist(chatID)
	if d.opts.OnCorrection != nil {
		d.opts.OnCorrection(chatID, desk.Correction{Proposed: proposed, Final: &final, Edited: edited != nil})
	}
	if _, err := d.newRun(chatID, final); err != nil {
		return err
	}
	if err := d.advance(ctx, chatID); err != nil && !errors.Is(err, agent.ErrBusy) {
		return err
	}
	return nil
}

// DeclinePlan drops a suggested plan.
func (d *Desk) DeclinePlan(chatID, planID string) error {
	d.mu.Lock()
	proposed, ok := d.chat(chatID).suggested[planID]
	d.chat(chatID).unsuggest(planID)
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("chatrunner: no suggested plan %q", planID)
	}
	d.persist(chatID)
	if d.opts.OnCorrection != nil {
		d.opts.OnCorrection(chatID, desk.Correction{Proposed: proposed})
	}
	return nil
}

// Complete records the outcome of a delegated item (see
// DeskOptions.Delegate) under the ref Delegate returned.
func (d *Desk) Complete(ctx context.Context, ref desk.Ref, ok bool, result string) error {
	d.mu.Lock()
	var run *planRun
	var item string
	for _, c := range d.chats {
		for _, p := range c.plans {
			if id, found := p.refs[ref]; found {
				run, item = p, id
			}
		}
	}
	d.mu.Unlock()
	if run == nil {
		return fmt.Errorf("chatrunner: no delegated work for %s", ref)
	}
	d.finish(run, item, ok, result)
	if err := d.advance(ctx, run.chatID); err != nil && !errors.Is(err, agent.ErrBusy) {
		return err
	}
	return nil
}

// answerOpen takes the owner's message as the answer to the desk's open
// questions on the chat that have reached the owner (a question the
// editor still holds has not been asked yet). The owner's next message is
// taken as the answer: the desk has no way to tell an answer from a new
// subject, so a plan with a question suits a conversation about that plan.
func (d *Desk) answerOpen(chatID, text string) {
	d.mu.Lock()
	type open struct {
		run      *planRun
		item, id string
	}
	var answered []open
	if c := d.chats[chatID]; c != nil {
		held := map[string]bool{}
		for _, q := range c.editor.Pending() {
			held[q.Item.ID] = true
		}
		for _, p := range c.plans {
			for item, q := range p.asked {
				if !held[p.workID(item)] {
					answered = append(answered, open{p, item, q})
				}
			}
		}
	}
	d.mu.Unlock()
	for _, a := range answered {
		_ = d.ledger.Answer(a.run.topic, a.id, text)
		d.touchLedger()
		d.mu.Lock()
		delete(a.run.asked, a.item)
		d.mu.Unlock()
		d.finish(a.run, a.item, true, text)
	}
}

// finish records an item's outcome in the plan and the ledger, and saves.
func (d *Desk) finish(run *planRun, item string, ok bool, result string) {
	d.record(run, item, ok, result)
	d.persist(run.chatID)
}

// record records an item's outcome in the plan and the ledger.
func (d *Desk) record(run *planRun, item string, ok bool, result string) {
	state := desk.WorkDone
	if !ok {
		state = desk.WorkFailed
	}
	d.mu.Lock()
	run.state[item] = state
	run.result[item] = result
	d.mu.Unlock()
	if err := d.ledger.Progress(run.topic, run.workID(item), state, result); err != nil {
		d.logger.Warn("chatrunner: desk ledger", "topic", run.topic, "work", item, "error", err.Error())
	}
	d.touchLedger()
}

// advance starts every ready item of the chat's plans. Subtask items are
// dispatched as background jobs; it needs the conversation idle and fails
// with agent.ErrBusy otherwise (the runner retries when the turn ends).
func (d *Desk) advance(ctx context.Context, chatID string) error {
	return d.advanceWith(ctx, chatID, nil)
}

// advanceWith starts ready items; with c set it dispatches on that
// conversation directly (the caller holds it, as a turn does before it
// sends), otherwise it takes the conversation like a host dispatch.
func (d *Desk) advanceWith(ctx context.Context, chatID string, c *agent.Conversation) error {
	if c == nil {
		// A turn holding the conversation (c set) needs no lock: nothing
		// else can dispatch on it meanwhile.
		d.advancing.Lock()
		defer d.advancing.Unlock()
	}
	if d.r.isClosed() {
		return nil
	}
	var subtasks []pick
	var asks []pick
	var delegates []pick
	d.mu.Lock()
	chat := d.chats[chatID]
	if chat == nil {
		d.mu.Unlock()
		return nil
	}
	for _, run := range chat.plans {
		for _, it := range run.ready() {
			switch {
			case it.Kind == desk.KindAnswer:
				// The chat agent answers it in the conversation.
				run.state[it.ID] = desk.WorkDone
			case it.Kind == desk.KindQuestion:
				run.state[it.ID] = desk.WorkRunning
				asks = append(asks, pick{run, it})
			case it.Kind == desk.KindDelegate && d.opts.Delegate != nil:
				run.state[it.ID] = desk.WorkRunning
				delegates = append(delegates, pick{run, it})
			default:
				run.state[it.ID] = desk.WorkRunning
				subtasks = append(subtasks, pick{run, it})
			}
		}
	}
	d.mu.Unlock()

	for _, p := range asks {
		q, err := d.ledger.Ask(p.run.topic, "", p.item.Goal)
		if err != nil {
			d.finish(p.run, p.item.ID, false, err.Error())
			continue
		}
		_ = d.ledger.Progress(p.run.topic, p.run.workID(p.item.ID), desk.WorkRunning, "")
		d.touchLedger()
		d.mu.Lock()
		p.run.asked[p.item.ID] = q.ID
		d.mu.Unlock()
		d.submit(chatID, desk.Item{ID: p.run.workID(p.item.ID), Topic: p.run.topic, Kind: desk.ItemQuestion, Text: p.item.Goal, Source: "desk"})
	}
	for _, p := range delegates {
		ref, err := d.opts.Delegate(ctx, chatID, p.run.topic, p.item)
		if err != nil {
			d.finish(p.run, p.item.ID, false, err.Error())
			d.submit(chatID, desk.Item{ID: p.run.workID(p.item.ID), Topic: p.run.topic, Kind: desk.ItemFailure, Title: p.item.Goal, Text: "could not hand it over: " + err.Error(), Source: "desk"})
			continue
		}
		_ = d.ledger.Progress(p.run.topic, p.run.workID(p.item.ID), desk.WorkRunning, "")
		_ = d.ledger.Link(p.run.topic, ref)
		d.touchLedger()
		d.mu.Lock()
		p.run.refs[ref] = p.item.ID
		d.mu.Unlock()
	}
	var err error
	if len(subtasks) > 0 {
		err = d.dispatch(ctx, chatID, c, subtasks)
		if err != nil {
			// Put them back to start later. A held conversation is
			// retried when its run ends; any other failure counts, and an
			// item that keeps failing is given up.
			busy := errors.Is(err, agent.ErrBusy)
			var failed []pick
			d.mu.Lock()
			for _, p := range subtasks {
				p.run.state[p.item.ID] = desk.WorkPlanned
				if !busy {
					p.run.attempts[p.item.ID]++
					if p.run.attempts[p.item.ID] >= maxDispatchAttempts {
						failed = append(failed, p)
					}
				}
			}
			d.mu.Unlock()
			for _, p := range failed {
				d.finish(p.run, p.item.ID, false, "could not start: "+err.Error())
				d.submit(chatID, desk.Item{ID: p.run.workID(p.item.ID), Topic: p.run.topic, Kind: desk.ItemFailure, Title: p.item.Goal, Text: "could not start: " + err.Error(), Source: "desk"})
			}
		}
	}
	d.prune(chatID)
	d.pump(chatID)
	return err
}

// pick is one plan item chosen to start.
type pick struct {
	run  *planRun
	item desk.PlanItem
}

func (d *Desk) dispatch(ctx context.Context, chatID string, c *agent.Conversation, picks []pick) error {
	starts := make([]agent.DispatchStart, len(picks))
	for i, p := range picks {
		starts[i] = agent.DispatchStart{Variant: d.variant(p.item.Kind), Goal: d.goal(p.run, p.item), Background: true,
			ReturnSpec: jsonString(returnSpec(p.item))}
	}
	var outcomes []agent.Dispatched
	var err error
	if c != nil {
		outcomes, err = dispatchOn(ctx, c, starts)
	} else {
		outcomes, err = d.r.hostDispatch(ctx, chatID, starts)
	}
	if err != nil {
		return err
	}
	for i, p := range picks {
		var handle struct{ ID string }
		if i >= len(outcomes) || outcomes[i].IsError || json.Unmarshal(outcomes[i].Result, &handle) != nil || handle.ID == "" {
			reason := "the subtask did not start"
			if i < len(outcomes) && outcomes[i].Err != "" {
				reason = outcomes[i].Err
			}
			d.finish(p.run, p.item.ID, false, reason)
			d.submit(chatID, desk.Item{ID: p.run.workID(p.item.ID), Topic: p.run.topic, Kind: desk.ItemFailure, Title: p.item.Goal, Text: reason, Source: "desk"})
			continue
		}
		ref := desk.Ref{Kind: "job", ID: handle.ID}
		_ = d.ledger.Progress(p.run.topic, p.run.workID(p.item.ID), desk.WorkRunning, "")
		_ = d.ledger.Link(p.run.topic, ref)
		d.touchLedger()
		d.mu.Lock()
		d.jobs[handle.ID] = p.run
		p.run.refs[ref] = p.item.ID
		d.mu.Unlock()
	}
	d.persist(chatID)
	return nil
}

// goal is a subtask's goal: the item's goal, what counts as done, and the
// results of the items it depends on.
func (d *Desk) goal(run *planRun, it desk.PlanItem) string {
	var b strings.Builder
	b.WriteString(it.Goal)
	if it.Expect != "" {
		b.WriteString("\n\nDone when: " + it.Expect)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, dep := range it.DependsOn {
		if r := run.result[dep]; r != "" {
			fmt.Fprintf(&b, "\n\nResult of an earlier step (%s):\n%s", dep, r)
		}
	}
	return b.String()
}

func jsonString(s string) json.RawMessage {
	raw, _ := json.Marshal(s)
	return raw
}

func returnSpec(it desk.PlanItem) string {
	if it.Expect != "" {
		return "A short report for the owner: " + it.Expect
	}
	return "A short report for the owner of what you found or did."
}

// dispatchOn dispatches on a conversation the caller holds, and returns
// once the starts have been made.
func dispatchOn(ctx context.Context, c *agent.Conversation, starts []agent.DispatchStart) ([]agent.Dispatched, error) {
	run, err := c.Dispatch(ctx, starts)
	if err != nil {
		return nil, err
	}
	for range run.Events() {
	}
	result, err := run.Wait()
	return result.Dispatched, err
}

// prune drops finished plans.
func (d *Desk) prune(chatID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.chats[chatID]
	if c == nil {
		return
	}
	keep := c.plans[:0]
	for _, p := range c.plans {
		if p.done() {
			for ref := range p.refs {
				if ref.Kind == "job" {
					delete(d.jobs, ref.ID)
				}
			}
			continue
		}
		keep = append(keep, p)
	}
	c.plans = keep
}

// notice takes one notice from the agent: a desk job's finish updates its
// plan and ledger and its result goes to the delivery editor, as does every
// other notice no turn the owner sees carried.
func (d *Desk) notice(ctx context.Context, n agent.NoticePosted) {
	chatID := d.r.chatID(n.ConversationID)
	if chatID == "" {
		return
	}
	run, job, retired := d.jobFor(n)
	if retired {
		return // its finish was applied already (Recover read it first)
	}
	if run != nil {
		if o := d.jobOutcome(ctx, n.ConversationID, job); o.finished {
			// Its result goes through the editor: a turn the owner sees
			// shows only that the job ended.
			d.settle(ctx, chatID, run, job, o)
			return
		}
		// Not its finish: a question or progress from it.
	}
	// A plain notice a turn the owner sees carried is already on the wire.
	if n.RunID != "" && d.r.activeRunID(n.ConversationID) == n.RunID && d.r.ownerTurn(n.ConversationID) {
		return
	}
	item := desk.Item{ID: "notice-" + n.ID, Kind: desk.ItemNotice, Text: n.Body, Source: n.Kind}
	if n.Kind == "subtask_ask" {
		item.Kind = desk.ItemQuestion
	}
	d.submit(chatID, item)
}

// jobLookupAttempts is how many times a finished job is read before its
// item is failed; a lookup can fail for a moment (a busy database).
// jobLookupBackoff is the first wait between attempts; it doubles.
const jobLookupAttempts = 3

var jobLookupBackoff = 500 * time.Millisecond

// subtaskJobKind is the kind of the notice a background subtask's finish
// posts.
const subtaskJobKind = "subagent"

// notIDRune separates the words of a notice when looking for a job ID.
func notIDRune(r rune) bool {
	idRune := r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_' || r == '-'
	return !idRune
}

// queue hands a notice to its conversation's worker, starting one when
// there is none.
func (d *Desk) queue(ctx context.Context, n agent.NoticePosted) {
	d.r.mu.Lock()
	defer d.r.mu.Unlock()
	if d.r.closed {
		return
	}
	d.mu.Lock()
	w := d.workers[n.ConversationID]
	d.mu.Unlock()
	if w != nil && w.push(n) {
		return
	}
	w = &noticeWorker{wake: make(chan struct{}, 1)}
	w.push(n)
	d.mu.Lock()
	d.workers[n.ConversationID] = w
	d.mu.Unlock()
	d.r.publishing.Add(1)
	go func(key string) {
		defer d.r.publishing.Done()
		w.run(ctx, d)
		d.mu.Lock()
		if d.workers[key] == w {
			delete(d.workers, key)
		}
		d.mu.Unlock()
	}(n.ConversationID)
}

func (w *noticeWorker) push(n agent.NoticePosted) bool {
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return false
	}
	w.queue = append(w.queue, n)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return true
}

func (w *noticeWorker) run(ctx context.Context, d *Desk) {
	idle := time.NewTimer(publisherIdle)
	defer idle.Stop()
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.mu.Unlock()
			select {
			case <-w.wake:
				continue
			case <-ctx.Done():
			case <-idle.C:
			}
			w.mu.Lock()
			if len(w.queue) == 0 || ctx.Err() != nil {
				w.done = true
				w.mu.Unlock()
				return
			}
			w.mu.Unlock()
			continue
		}
		n := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		d.notice(ctx, n)
		idle.Reset(publisherIdle)
	}
}

// jobFor finds the desk job whose finish a notice reports: a subtask job
// notice naming the job's ID as a word. retired is set when the notice
// names a job whose finish the desk has already applied.
func (d *Desk) jobFor(n agent.NoticePosted) (run *planRun, job string, retired bool) {
	if n.Kind != subtaskJobKind {
		return nil, "", false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, word := range strings.FieldsFunc(n.Body, notIDRune) {
		if run := d.jobs[word]; run != nil {
			return run, word, false
		}
		if d.retired[word] {
			return nil, word, true
		}
	}
	return nil, "", false
}

// outcome is how a desk job ended, as jobOutcome read it.
type outcome struct {
	finished bool
	ok       bool
	// interrupted is set when Nanite stopping ended the job (Close, or a
	// process exit), not the job itself: the desk may start it again.
	interrupted bool
	text        string
}

// jobOutcome reads a job: whether it has finished, whether it succeeded,
// and its result as text for the owner and later steps.
func (d *Desk) jobOutcome(ctx context.Context, naniteID, job string) outcome {
	c, err := d.r.agent.Conversation(ctx, naniteID)
	if err != nil {
		return outcome{finished: true, text: err.Error()}
	}
	var raw string
	for attempt := range jobLookupAttempts {
		if raw, err = c.Job(ctx, job); err == nil || ctx.Err() != nil {
			break
		}
		if attempt < jobLookupAttempts-1 {
			select {
			case <-time.After(jobLookupBackoff << attempt):
			case <-ctx.Done():
			}
		}
	}
	if err != nil {
		return outcome{finished: true, text: err.Error()}
	}
	var view struct {
		Status string          `json:"status"`
		Reason string          `json:"reason"`
		Result json.RawMessage `json:"result"`
		Output string          `json:"output"`
	}
	if json.Unmarshal([]byte(raw), &view) != nil {
		return outcome{finished: true, text: raw}
	}
	if view.Status == "running" || view.Status == "queued" || view.Status == "" {
		return outcome{}
	}
	text := resultText(view.Result)
	if text == "" {
		text = strings.TrimSpace(view.Output)
	}
	o := outcome{finished: true, ok: view.Status == "ok",
		interrupted: view.Reason == agent.JobReasonShutdown && view.Status == "cancelled" || view.Reason == agent.JobReasonProcessExit}
	if !o.ok && view.Reason != "" {
		text = strings.TrimSpace(view.Status + " (" + view.Reason + ") " + text)
	} else if !o.ok {
		text = strings.TrimSpace(view.Status + " " + text)
	}
	o.text = clip(text, 4000)
	return o
}

// resultText is a subtask result as text: a string as is, an object's
// usual report fields, or the JSON.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		for _, key := range []string{"result", "report", "summary", "answer", "findings", "outcome"} {
			if v, ok := obj[key]; ok {
				if t := resultText(v); t != "" {
					return t
				}
			}
		}
	}
	return strings.TrimSpace(string(raw))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(s, n)
}

// submit queues an item on a chat's editor and publishes what is due.
func (d *Desk) submit(chatID string, it desk.Item) {
	d.mu.Lock()
	c := d.chat(chatID)
	d.mu.Unlock()
	c.editor.Submit(it, d.state(chatID))
	d.pump(chatID)
}

// withdraw removes an obsolete queued notice and persists the editor state.
func (d *Desk) withdraw(chatID, itemID string) {
	d.mu.Lock()
	c := d.chats[chatID]
	d.mu.Unlock()
	if c == nil {
		return
	}
	c.editor.Remove(itemID)
	d.pump(chatID)
}

func (d *Desk) state(chatID string) desk.State {
	d.mu.Lock()
	typing := d.chat(chatID).typing
	d.mu.Unlock()
	return desk.State{Mode: d.mode(chatID), Busy: typing || d.r.ownerTurn(ConversationID(chatID))}
}

// pump publishes what a chat's editor has due and sets its timer for the
// next item due by time.
func (d *Desk) pump(chatID string) {
	if d.r.isClosed() {
		return
	}
	d.mu.Lock()
	c := d.chats[chatID]
	d.mu.Unlock()
	if c == nil {
		return
	}
	st := d.state(chatID)
	if m, ok := c.editor.Due(st); ok {
		// Save that it was released before it goes out: a crash in
		// between loses the message rather than sending it twice.
		d.persist(chatID)
		d.publish(chatID, m)
	}
	next, ok := c.editor.Next(st)
	d.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if ok {
		wait := max(time.Until(next), 10*time.Millisecond)
		c.timer = time.AfterFunc(wait, func() { d.pump(chatID) })
	}
	d.mu.Unlock()
	d.persist(chatID)
}

// publish puts one release on the wire as a desk notice, in order with
// the chat's other notices.
func (d *Desk) publish(chatID string, m desk.Message) {
	d.mu.Lock()
	id := d.nextID("desk-")
	d.mu.Unlock()
	d.r.enqueueNotice(agent.NoticePosted{ID: id, ConversationID: ConversationID(chatID), Kind: NoticeKindDesk, Body: m.Text})
}

// idle runs when a chat's turn ends: start what waited for the
// conversation, and publish what waited for the owner.
func (d *Desk) idle(chatID string) {
	if err := d.advance(context.Background(), chatID); err != nil && !errors.Is(err, agent.ErrBusy) {
		d.logger.Warn("chatrunner: desk advance", "conversation", chatID, "error", err.Error())
	}
}

// stop stops the editors' timers.
func (d *Desk) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.chats {
		if c.timer != nil {
			c.timer.Stop()
		}
	}
}
