package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/agent/desk"
	"github.com/hollis-labs/nanite/agent"
)

// The front desk keeps its state in the agent's host state, so a restart
// picks up where it stopped: each chat's plans, suggested plans and
// delivery queue on its Nanite conversation, and the ledger (when the desk
// owns it) agent-wide.
const (
	deskStateKey   = "desk"
	deskLedgerKey  = "desk.ledger"
	deskStateV1    = 1
	maxRetiredJobs = 256
	// A suggested plan nobody answered is dropped after suggestionTTL, and
	// a chat keeps at most maxSuggestions (the oldest go first).
	suggestionTTL  = 24 * time.Hour
	maxSuggestions = 20
)

type savedChat struct {
	Version     int                  `json:"version"`
	ChatID      string               `json:"chat_id"`
	Seq         int                  `json:"seq"`
	Suggested   map[string]desk.Plan `json:"suggested,omitempty"`
	SuggestedAt map[string]time.Time `json:"suggested_at,omitempty"`
	Plans       []savedPlan          `json:"plans,omitempty"`
	Editor      desk.Snapshot        `json:"editor"`
	// Retired is the jobs whose finish the desk has applied, so a notice
	// for one that arrives later (or again) is dropped.
	Retired []string `json:"retired,omitempty"`
}

type savedPlan struct {
	ID       string                    `json:"id"`
	Topic    desk.TopicID              `json:"topic"`
	Plan     desk.Plan                 `json:"plan"`
	State    map[string]desk.WorkState `json:"state"`
	Result   map[string]string         `json:"result,omitempty"`
	Attempts map[string]int            `json:"attempts,omitempty"`
	Asked    map[string]string         `json:"asked,omitempty"`
	Refs     []savedRef                `json:"refs,omitempty"`
}

type savedRef struct {
	Ref  desk.Ref `json:"ref"`
	Item string   `json:"item"`
}

// savedLedger wraps the ledger's own JSON with the desk's state version.
type savedLedger struct {
	Version int             `json:"version"`
	Ledger  json.RawMessage `json:"ledger"`
}

// empty reports whether the chat has nothing worth keeping. Retired jobs
// count: without them a job notice replayed later would reach the owner
// as a raw notice.
func (s savedChat) empty() bool {
	return len(s.Suggested) == 0 && len(s.Plans) == 0 && len(s.Editor.Pending) == 0 && len(s.Editor.Filed) == 0 && len(s.Retired) == 0
}

// snapshotLocked is a chat's state to save; d.mu is held.
func (d *Desk) snapshotLocked(chatID string, c *deskChat) savedChat {
	// Copies: the state is marshaled after d.mu is released.
	s := savedChat{Version: deskStateV1, ChatID: chatID, Seq: d.seq, Suggested: maps.Clone(c.suggested), SuggestedAt: maps.Clone(c.suggestedAt),
		Retired: slices.Clone(c.retired)}
	for _, p := range c.plans {
		sp := savedPlan{ID: p.id, Topic: p.topic, Plan: p.plan, State: maps.Clone(p.state), Result: maps.Clone(p.result),
			Attempts: maps.Clone(p.attempts), Asked: maps.Clone(p.asked)}
		for ref, item := range p.refs {
			sp.Refs = append(sp.Refs, savedRef{Ref: ref, Item: item})
		}
		slices.SortFunc(sp.Refs, func(a, b savedRef) int { return strings.Compare(a.Ref.Kind+"/"+a.Ref.ID, b.Ref.Kind+"/"+b.Ref.ID) })
		s.Plans = append(s.Plans, sp)
	}
	return s
}

// persist saves a chat's state, and the ledger when the desk owns it.
// Saves are serialized and each reads the state when it runs, so an older
// state never overwrites a newer one; d.mu is not held while writing.
//
// The chat's row and the ledger's are two writes. A crash between them
// leaves the ledger one change behind the plans, which only the ledger's
// view shows: plans and jobs are driven by the chat's row, and Recover
// brings the ledger's work states back in line with the plans.
func (d *Desk) persist(chatID string) {
	if d.r.agent == nil {
		return
	}
	d.saveMu.Lock()
	defer d.saveMu.Unlock()
	ctx := context.Background()
	d.mu.Lock()
	c := d.chats[chatID]
	if c == nil {
		d.mu.Unlock()
		return
	}
	s := d.snapshotLocked(chatID, c)
	d.mu.Unlock()
	if !c.frozen {
		s.Editor = c.editor.Snapshot()
		raw := ""
		if !s.empty() {
			b, err := json.Marshal(s)
			if err != nil {
				d.logger.Warn("chatrunner: desk state", "conversation", chatID, "error", err.Error())
				return
			}
			raw = string(b)
		}
		if raw != c.saved {
			if err := d.write(ctx, chatID, raw); err != nil {
				d.saveFailed(chatID, err)
				return
			}
			c.saved = raw
		}
	}
	d.persistLedger(ctx)
}

func (d *Desk) write(ctx context.Context, chatID, raw string) error {
	conv, err := d.r.agent.Conversation(ctx, d.r.naniteID(chatID))
	if errors.Is(err, agent.ErrNoConversation) && raw == "" {
		return nil
	}
	if err != nil {
		return err
	}
	return conv.SetHostState(ctx, deskStateKey, raw)
}

// touchLedger records a change to the ledger, so the next save writes it.
// Changes a host makes through Desk.Ledger directly are saved with the
// desk's next change.
func (d *Desk) touchLedger() {
	d.mu.Lock()
	d.ledgerGen++
	d.mu.Unlock()
}

// persistLedger saves the ledger the desk owns, when it has changed since
// the last save; saveMu is held.
func (d *Desk) persistLedger(ctx context.Context) {
	if !d.ownLedger || d.ledgerFrozen {
		return
	}
	d.mu.Lock()
	gen := d.ledgerGen
	d.mu.Unlock()
	if gen == d.savedLedgerGen {
		return
	}
	inner, err := d.ledger.MarshalJSON()
	if err != nil {
		d.logger.Warn("chatrunner: desk ledger state", "error", err.Error())
		return
	}
	b, err := json.Marshal(savedLedger{Version: deskStateV1, Ledger: inner})
	if err != nil {
		d.logger.Warn("chatrunner: desk ledger state", "error", err.Error())
		return
	}
	if err := d.r.agent.SetHostState(ctx, deskLedgerKey, string(b)); err != nil {
		d.saveFailed("", err)
		return
	}
	d.savedLedgerGen = gen
}

func (d *Desk) saveFailed(chatID string, err error) {
	if d.r.isClosed() {
		return // a late save after Close; Close saved everything first
	}
	d.logger.Warn("chatrunner: save desk state", "conversation", chatID, "error", err.Error())
}

// saveAll saves every chat, before the agent closes.
func (d *Desk) saveAll() {
	d.mu.Lock()
	ids := make([]string, 0, len(d.chats))
	for id := range d.chats {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.persist(id)
	}
}

// load reads the saved state back: the ledger the desk owns, and each
// chat's plans, suggested plans and delivery queue. It runs before the
// runner listens for notices; Recover then settles the jobs. State written
// by a newer Nanite is left alone: it is neither read nor overwritten.
func (d *Desk) load(ctx context.Context) error {
	if d.ownLedger {
		raw, err := d.r.agent.HostState(ctx, deskLedgerKey)
		if err != nil {
			return fmt.Errorf("chatrunner: load desk ledger: %w", err)
		}
		if raw != "" {
			var saved savedLedger
			switch err := json.Unmarshal([]byte(raw), &saved); {
			case err == nil && saved.Version > deskStateV1:
				d.ledgerFrozen = true
				d.logger.Warn("chatrunner: saved desk ledger is from a newer version; not read and not saved", "version", saved.Version)
			case err != nil || saved.Version != deskStateV1:
				d.logger.Warn("chatrunner: saved desk ledger unreadable; starting empty")
			default:
				if err := d.ledger.UnmarshalJSON(saved.Ledger); err != nil {
					d.logger.Warn("chatrunner: saved desk ledger unreadable; starting empty", "error", err.Error())
				}
			}
		}
	}
	states, err := d.r.agent.ConversationHostStates(ctx, deskStateKey)
	if err != nil {
		return fmt.Errorf("chatrunner: load desk state: %w", err)
	}
	for naniteID, raw := range states {
		var head struct {
			Version int    `json:"version"`
			ChatID  string `json:"chat_id"`
		}
		if json.Unmarshal([]byte(raw), &head) == nil && head.Version > deskStateV1 && head.ChatID != "" && d.r.naniteID(head.ChatID) == naniteID {
			d.mu.Lock()
			d.chat(head.ChatID).frozen = true
			d.mu.Unlock()
			d.logger.Warn("chatrunner: saved desk state is from a newer version; not read and not saved", "conversation", naniteID, "version", head.Version)
			continue
		}
		var s savedChat
		if err := json.Unmarshal([]byte(raw), &s); err == nil && s.ChatID != "" && d.r.naniteID(s.ChatID) != naniteID && d.r.olderGeneration(s.ChatID, naniteID) {
			continue // the conversation that served the chat before a ResetChat
		}
		if err := json.Unmarshal([]byte(raw), &s); err != nil || s.Version != deskStateV1 || s.ChatID == "" || d.r.naniteID(s.ChatID) != naniteID {
			d.logger.Warn("chatrunner: saved desk state unreadable; dropped", "conversation", naniteID)
			continue
		}
		d.restore(naniteID, s, raw)
	}
	return nil
}

func (d *Desk) restore(naniteID string, s savedChat, raw string) {
	d.mu.Lock()
	c := d.chat(s.ChatID)
	if err := c.editor.Restore(s.Editor); err != nil {
		d.logger.Warn("chatrunner: saved desk queue unreadable; dropped", "conversation", naniteID, "error", err.Error())
	}
	d.seq = max(d.seq, s.Seq)
	for id, p := range s.Suggested {
		c.suggested[id] = p
		c.suggestedAt[id] = s.SuggestedAt[id]
	}
	c.expireSuggestions(time.Now())
	for _, sp := range s.Plans {
		p := &planRun{id: sp.ID, chatID: s.ChatID, topic: sp.Topic, plan: sp.Plan, state: sp.State, result: sp.Result,
			attempts: sp.Attempts, asked: sp.Asked, refs: map[desk.Ref]string{}}
		ensure(&p.state)
		ensure(&p.result)
		ensure(&p.attempts)
		ensure(&p.asked)
		started := map[string]bool{}
		for _, r := range sp.Refs {
			p.refs[r.Ref] = r.Item
			started[r.Item] = true
			if r.Ref.Kind == "job" {
				d.jobs[r.Ref.ID] = p
			}
		}
		for item := range p.asked {
			started[item] = true
		}
		// A step is marked running before its job (or hand-off, or
		// question) is recorded, and a save can land in between. With
		// nothing recorded it never started as far as the desk knows: it
		// goes back to planned, without counting an attempt.
		for item, st := range p.state {
			if st == desk.WorkRunning && !started[item] {
				p.state[item] = desk.WorkPlanned
			}
		}
		c.plans = append(c.plans, p)
	}
	for _, job := range s.Retired {
		d.retireLocked(c, job)
	}
	c.saved = raw
	d.mu.Unlock()
	d.r.mu.Lock()
	d.r.chats[naniteID] = s.ChatID
	d.r.mu.Unlock()
}

// ensure makes a nil map empty.
func ensure[K comparable, V any](m *map[K]V) {
	if *m == nil {
		*m = map[K]V{}
	}
}

// retireLocked records a job whose finish has been applied, forgetting the
// oldest past maxRetiredJobs; d.mu is held.
func (d *Desk) retireLocked(c *deskChat, job string) {
	if d.retired[job] {
		return
	}
	d.retired[job] = true
	c.retired = append(c.retired, job)
	if over := len(c.retired) - maxRetiredJobs; over > 0 {
		for _, old := range c.retired[:over] {
			delete(d.retired, old)
		}
		c.retired = slices.Clone(c.retired[over:])
	}
}

// suggest records a suggested plan; d.mu is held.
func (c *deskChat) suggest(id string, p desk.Plan, now time.Time) {
	c.suggested[id] = p
	c.suggestedAt[id] = now
	c.expireSuggestions(now)
}

// unsuggest drops a suggested plan; d.mu is held.
func (c *deskChat) unsuggest(id string) {
	delete(c.suggested, id)
	delete(c.suggestedAt, id)
}

// expireSuggestions drops suggested plans older than suggestionTTL, then
// the oldest past maxSuggestions; d.mu is held.
func (c *deskChat) expireSuggestions(now time.Time) {
	for id, at := range c.suggestedAt {
		if now.Sub(at) > suggestionTTL {
			c.unsuggest(id)
		}
	}
	for id := range c.suggested {
		if _, ok := c.suggestedAt[id]; !ok {
			c.suggestedAt[id] = now // saved without a time: start its clock now
		}
	}
	for len(c.suggested) > maxSuggestions {
		oldest := ""
		for id, at := range c.suggestedAt {
			if oldest == "" || at.Before(c.suggestedAt[oldest]) {
				oldest = id
			}
		}
		c.unsuggest(oldest)
	}
}

// Recover settles what the previous process left in flight: a job that
// finished meanwhile has its outcome applied, a job a restart stopped is
// started again (it counts as a failed attempt), and ready steps start.
// Queued deliveries that are due go out. The runner calls it on Open; it
// is safe to call again.
func (d *Desk) Recover(ctx context.Context) error {
	type inflight struct {
		run      *planRun
		chatID   string
		naniteID string
		job      string
	}
	var jobs []inflight
	var plans []*planRun
	d.mu.Lock()
	chats := make([]string, 0, len(d.chats))
	for chatID, c := range d.chats {
		chats = append(chats, chatID)
		for _, p := range c.plans {
			plans = append(plans, p)
			for ref, item := range p.refs {
				if ref.Kind == "job" && p.state[item] == desk.WorkRunning {
					jobs = append(jobs, inflight{p, chatID, d.r.naniteID(chatID), ref.ID})
				}
			}
		}
	}
	d.mu.Unlock()
	slices.Sort(chats)
	for _, p := range plans {
		d.syncLedger(p)
	}
	var errs []error
	for _, j := range jobs {
		o := d.jobOutcome(ctx, j.naniteID, j.job)
		if !o.finished {
			continue // still running in this process: its notice will come
		}
		d.settle(ctx, j.chatID, j.run, j.job, o)
	}
	for _, chatID := range chats {
		if err := d.advance(ctx, chatID); err != nil && !errors.Is(err, agent.ErrBusy) {
			errs = append(errs, fmt.Errorf("chatrunner: recover %s: %w", chatID, err))
		}
		d.pump(chatID)
		d.persist(chatID)
	}
	return errors.Join(errs...)
}

// syncLedger brings the ledger's work states in line with a plan's, after
// a crash between the two saves left the ledger behind.
func (d *Desk) syncLedger(p *planRun) {
	t, err := d.ledger.Get(p.topic)
	if err != nil {
		return
	}
	have := map[string]desk.WorkState{}
	for _, w := range t.Work {
		have[w.ID] = w.State
	}
	d.mu.Lock()
	want := maps.Clone(p.state)
	results := maps.Clone(p.result)
	d.mu.Unlock()
	for item, st := range want {
		if got, ok := have[p.workID(item)]; ok && got != st && st != desk.WorkPlanned {
			if d.ledger.Progress(p.topic, p.workID(item), st, results[item]) == nil {
				d.touchLedger()
			}
		}
	}
}

// settle applies a desk job's finish once: its result or failure goes to
// the plan, the ledger and the delivery editor, and a job a restart stopped
// is put back to start again. A job already settled (by a notice, or by
// Recover) is left alone. The result is queued before the state is saved,
// so a crash cannot record the step done without its result to deliver.
func (d *Desk) settle(ctx context.Context, chatID string, run *planRun, job string, o outcome) {
	ref := desk.Ref{Kind: "job", ID: job}
	d.mu.Lock()
	item, live := run.refs[ref]
	if !live || d.jobs[job] != run {
		d.mu.Unlock()
		return
	}
	c := d.chat(chatID)
	delete(run.refs, ref)
	delete(d.jobs, job)
	d.retireLocked(c, job)
	retry := false
	if o.interrupted {
		run.attempts[item]++
		if run.attempts[item] < maxDispatchAttempts {
			run.state[item] = desk.WorkPlanned
			retry = true
		}
	}
	d.mu.Unlock()
	goal, prefer := item, desk.Action("")
	for _, it := range run.plan.Items {
		if it.ID == item {
			goal, prefer = it.Goal, it.Delivery
		}
	}
	switch {
	case retry:
		d.logger.Info("chatrunner: desk job stopped by a restart; starting it again", "conversation", chatID, "job", job)
	case o.interrupted:
		text := "stopped by a restart too many times: " + o.text
		d.record(run, item, false, text)
		c.editor.Submit(desk.Item{ID: run.workID(item), Topic: run.topic, Kind: desk.ItemFailure, Title: goal, Text: text, Source: "desk", Prefer: prefer}, d.state(chatID))
	default:
		d.record(run, item, o.ok, o.text)
		kind := desk.ItemResult
		if !o.ok {
			kind = desk.ItemFailure
		}
		c.editor.Submit(desk.Item{ID: run.workID(item), Topic: run.topic, Kind: kind, Title: goal, Text: o.text, Source: "desk", Prefer: prefer}, d.state(chatID))
	}
	d.persist(chatID)
	if err := d.advance(ctx, chatID); err != nil && !errors.Is(err, agent.ErrBusy) {
		d.logger.Warn("chatrunner: desk advance", "conversation", chatID, "error", err.Error())
	}
}
