package chatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

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
)

type savedChat struct {
	Version   int                  `json:"version"`
	ChatID    string               `json:"chat_id"`
	Seq       int                  `json:"seq"`
	Suggested map[string]desk.Plan `json:"suggested,omitempty"`
	Plans     []savedPlan          `json:"plans,omitempty"`
	Editor    desk.Snapshot        `json:"editor"`
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

// empty reports whether the chat has nothing worth keeping.
func (s savedChat) empty() bool {
	return len(s.Suggested) == 0 && len(s.Plans) == 0 && len(s.Editor.Pending) == 0 && len(s.Editor.Filed) == 0
}

// snapshotLocked is a chat's state to save; d.mu is held.
func (d *Desk) snapshotLocked(chatID string, c *deskChat) savedChat {
	// Copies: the state is marshaled after d.mu is released.
	s := savedChat{Version: deskStateV1, ChatID: chatID, Seq: d.seq, Suggested: maps.Clone(c.suggested), Retired: slices.Clone(c.retired)}
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
	d.persistLedger(ctx)
}

func (d *Desk) write(ctx context.Context, chatID, raw string) error {
	conv, err := d.r.agent.Conversation(ctx, ConversationID(chatID))
	if errors.Is(err, agent.ErrNoConversation) && raw == "" {
		return nil
	}
	if err != nil {
		return err
	}
	return conv.SetHostState(ctx, deskStateKey, raw)
}

// persistLedger saves the ledger the desk owns; saveMu is held.
func (d *Desk) persistLedger(ctx context.Context) {
	if !d.ownLedger {
		return
	}
	b, err := d.ledger.MarshalJSON()
	if err != nil {
		d.logger.Warn("chatrunner: desk ledger state", "error", err.Error())
		return
	}
	if string(b) == d.savedLedger {
		return
	}
	if err := d.r.agent.SetHostState(ctx, deskLedgerKey, string(b)); err != nil {
		d.saveFailed("", err)
		return
	}
	d.savedLedger = string(b)
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
// runner listens for notices; Recover then settles the jobs.
func (d *Desk) load(ctx context.Context) error {
	if d.ownLedger {
		raw, err := d.r.agent.HostState(ctx, deskLedgerKey)
		if err != nil {
			return fmt.Errorf("chatrunner: load desk ledger: %w", err)
		}
		if raw != "" {
			if err := d.ledger.UnmarshalJSON([]byte(raw)); err != nil {
				d.logger.Warn("chatrunner: saved desk ledger unreadable; starting empty", "error", err.Error())
			} else {
				d.savedLedger = raw
			}
		}
	}
	states, err := d.r.agent.ConversationHostStates(ctx, deskStateKey)
	if err != nil {
		return fmt.Errorf("chatrunner: load desk state: %w", err)
	}
	for naniteID, raw := range states {
		var s savedChat
		if err := json.Unmarshal([]byte(raw), &s); err != nil || s.Version != deskStateV1 || s.ChatID == "" || ConversationID(s.ChatID) != naniteID {
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
	ensure(&c.suggested)
	for id, p := range s.Suggested {
		c.suggested[id] = p
	}
	for _, sp := range s.Plans {
		p := &planRun{id: sp.ID, chatID: s.ChatID, topic: sp.Topic, plan: sp.Plan, state: sp.State, result: sp.Result,
			attempts: sp.Attempts, asked: sp.Asked, refs: map[desk.Ref]string{}}
		ensure(&p.state)
		ensure(&p.result)
		ensure(&p.attempts)
		ensure(&p.asked)
		for _, r := range sp.Refs {
			p.refs[r.Ref] = r.Item
			if r.Ref.Kind == "job" {
				d.jobs[r.Ref.ID] = p
			}
		}
		c.plans = append(c.plans, p)
	}
	for _, job := range s.Retired {
		c.retire(job)
		d.retired[job] = true
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

// retire records a job whose finish has been applied; d.mu is held.
func (c *deskChat) retire(job string) {
	c.retired = append(c.retired, job)
	if len(c.retired) > maxRetiredJobs {
		c.retired = slices.Clone(c.retired[len(c.retired)-maxRetiredJobs:])
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
	d.mu.Lock()
	chats := make([]string, 0, len(d.chats))
	for chatID, c := range d.chats {
		chats = append(chats, chatID)
		for _, p := range c.plans {
			for ref, item := range p.refs {
				if ref.Kind == "job" && p.state[item] == desk.WorkRunning {
					jobs = append(jobs, inflight{p, chatID, ConversationID(chatID), ref.ID})
				}
			}
		}
	}
	d.mu.Unlock()
	slices.Sort(chats)
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

// settle applies a desk job's finish once: its result or failure goes to
// the plan, the ledger and the delivery editor, and a job a restart stopped
// is put back to start again. A job already settled (by a notice, or by
// Recover) is left alone.
func (d *Desk) settle(ctx context.Context, chatID string, run *planRun, job string, o outcome) {
	ref := desk.Ref{Kind: "job", ID: job}
	d.mu.Lock()
	item, live := run.refs[ref]
	if !live || d.jobs[job] != run {
		d.mu.Unlock()
		return
	}
	delete(run.refs, ref)
	delete(d.jobs, job)
	d.retired[job] = true
	d.chat(chatID).retire(job)
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
		_ = d.ledger.Progress(run.topic, run.workID(item), desk.WorkPlanned, "")
		d.logger.Info("chatrunner: desk job stopped by a restart; starting it again", "conversation", chatID, "job", job)
	case o.interrupted:
		text := "stopped by a restart too many times: " + o.text
		d.finish(run, item, false, text)
		d.submit(chatID, desk.Item{ID: run.workID(item), Topic: run.topic, Kind: desk.ItemFailure, Title: goal, Text: text, Source: "desk", Prefer: prefer})
	default:
		d.finish(run, item, o.ok, o.text)
		kind := desk.ItemResult
		if !o.ok {
			kind = desk.ItemFailure
		}
		d.submit(chatID, desk.Item{ID: run.workID(item), Topic: run.topic, Kind: kind, Title: goal, Text: o.text, Source: "desk", Prefer: prefer})
	}
	if err := d.advance(ctx, chatID); err != nil && !errors.Is(err, agent.ErrBusy) {
		d.logger.Warn("chatrunner: desk advance", "conversation", chatID, "error", err.Error())
	}
}
