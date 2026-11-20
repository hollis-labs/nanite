package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/agent/core/reflexes"
	"github.com/hollis-labs/libs/agent/steer"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

// Steering configures the steering a host wants Nanite to run before each
// root turn (libs agent/steer): modes and presets, work slots, reflexes and
// a classifier. Nil Config.Steering turns it off.
type Steering struct {
	// Registry holds the presets. Required.
	Registry *steer.Registry
	// Vocabulary is the reflex action kinds; nil holds only remind and halt.
	Vocabulary *steer.Vocabulary
	// Policy is the classifier policy; nil is steer.DefaultPolicy, which
	// applies nothing on its own.
	Policy steer.Policy
	// NewSelector makes a conversation's work-slot selector; nil is a focus
	// selector that falls back to GeneralSlot.
	NewSelector func() steer.Selector
	// Slots returns the work slots the host offers now; nil offers none.
	Slots func() []steer.Slot
	// Reflexes returns the host's reflexes now; nil holds none. Invalid ones
	// are skipped with a warning.
	Reflexes func() []steer.Reflex
	// Classify, when set, classifies an owner's message. Its result is only
	// suggested or recorded unless Policy sets a facet to auto.
	Classify func(ctx context.Context, sit steer.Situation, slots []steer.Slot) (steer.Classification, error)
	// StateFill, when set, adds host facts (unread mail, prefix tokens, scope
	// tier) to what predicate reflexes see.
	StateFill steer.StateFill
}

// GeneralSlot is the work slot in use when nothing more specific is.
var GeneralSlot = steer.Slot{Name: "general", About: "whatever is in front of the owner"}

// ErrNoSteering is returned by the mode calls when steering is not configured.
var ErrNoSteering = errors.New("nanite: steering is not configured")

// The conversation settings steering keeps: its deliberate mode and when its
// reflexes last fired, so a restart neither forgets the mode nor re-fires a
// reflex that is cooling down.
const (
	settingMode      = "mode"
	settingCooldowns = "reflex_cooldowns"
)

// classifyTimeout bounds the host's classifier on each person's message.
var classifyTimeout = 15 * time.Second

// steererIdle is how long a conversation's steering state is kept in memory
// unused. Its mode and cooldowns are kept in the store; a slot pin is not.
var steererIdle = 30 * time.Minute

// steering is one root conversation's steering state.
type steering struct {
	steer     *steer.Steer
	owner     *steer.OwnerControls
	cooldowns *steer.MemoryCooldowns
	// mu orders the owner's changes with what the store keeps, so the mode
	// in memory is the mode on disk; it also guards last.
	mu sync.Mutex
	// last is the classified preset the latest person's turn applied. The
	// notice and resumed turns after it keep that mode.
	last *steer.FacetResult
	// used is when the state was last asked for, under Engine.steerMu.
	used time.Time
}

func (s *Steering) validate() error {
	if s.Registry == nil {
		return errors.New("steering: Registry is required")
	}
	if s.Policy != nil {
		if err := s.Policy.Validate(); err != nil {
			return fmt.Errorf("steering: %w", err)
		}
	}
	return nil
}

// steerer is a root conversation's steering state: its mode, its selector's
// pin and its reflexes' cooldowns. It is made on first use, with the mode and
// cooldowns kept for the conversation, and dropped once it idles.
func (e *Engine) steerer(ctx context.Context, conversation string) (*steering, error) {
	cfg := e.cfg.Steering
	if cfg == nil {
		return nil, ErrNoSteering
	}
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	now := time.Now()
	for id, st := range e.steerers {
		if id != conversation && now.Sub(st.used) > steererIdle {
			delete(e.steerers, id)
		}
	}
	if st, ok := e.steerers[conversation]; ok {
		st.used = now
		return st, nil
	}
	vocab := cfg.Vocabulary
	if vocab == nil {
		vocab = steer.NewVocabulary()
	}
	var sel steer.Selector = steer.NewFocusSelector(GeneralSlot)
	if cfg.NewSelector != nil {
		sel = cfg.NewSelector()
	}
	cooldowns := steer.NewMemoryCooldowns()
	if raw, err := e.cfg.Store.Setting(ctx, conversation, settingCooldowns); err != nil {
		return nil, err
	} else if raw != "" {
		var snap map[string]time.Time
		if json.Unmarshal([]byte(raw), &snap) == nil {
			cooldowns.Restore(snap)
		}
	}
	opts := []steer.Option{steer.WithCooldowns(cooldowns), steer.WithAgentID(conversation)}
	if cfg.Policy != nil {
		opts = append(opts, steer.WithPolicy(cfg.Policy))
	}
	if cfg.StateFill != nil {
		opts = append(opts, steer.WithStateFill(cfg.StateFill))
	}
	s, owner, err := steer.New(cfg.Registry, sel, vocab, opts...)
	if err != nil {
		return nil, err
	}
	mode, err := e.cfg.Store.Setting(ctx, conversation, settingMode)
	if err != nil {
		return nil, err
	}
	if mode != "" {
		if err := owner.SetMode(mode); err != nil {
			// The preset is gone; the conversation falls back as if unset.
			e.cfg.Logger.WarnContext(ctx, "a conversation's mode is no longer registered", "conversation", conversation, "mode", mode)
		}
	}
	st := &steering{steer: s, owner: owner, cooldowns: cooldowns, used: now}
	e.steerers[conversation] = st
	return st, nil
}

// rootSteerer is a root conversation's steering state; a subtask has none.
func (e *Engine) rootSteerer(ctx context.Context, id string) (*steering, error) {
	if e.cfg.Steering == nil {
		return nil, ErrNoSteering
	}
	info, err := e.cfg.Store.Conversation(ctx, id)
	if err != nil {
		return nil, err
	}
	if info.ParentID != "" {
		return nil, errors.New("nanite: only a root conversation has a mode")
	}
	var st *steering
	err = e.guardHost(id, func() (err error) {
		st, err = e.steerer(ctx, id)
		return err
	})
	return st, err
}

// knownPreset refuses a name the registry does not hold; "" is no mode.
func (e *Engine) knownPreset(name string) error {
	if name == "" {
		return nil
	}
	if _, _, ok := e.cfg.Steering.Registry.Get(name); !ok {
		return fmt.Errorf("%w: %s", steer.ErrUnknownPreset, name)
	}
	return nil
}

// keepMode records a conversation's deliberate mode, then applies it with
// apply; if apply fails, the record goes back to prev. The caller holds st.mu.
func (e *Engine) keepMode(ctx context.Context, conversation, prev, name string, apply func() error) error {
	if err := e.cfg.Store.SetSetting(ctx, conversation, settingMode, name); err != nil {
		return err
	}
	if err := apply(); err != nil {
		return errors.Join(err, e.cfg.Store.SetSetting(context.WithoutCancel(ctx), conversation, settingMode, prev))
	}
	return nil
}

// SetMode sets a root conversation's deliberate mode for the owner; "" clears
// it. It is kept across restarts. There is no model tool for this: a host
// calls it when the person picks a mode.
func (e *Engine) SetMode(ctx context.Context, conversation, name string) error {
	st, err := e.rootSteerer(ctx, conversation)
	if err != nil {
		return err
	}
	if err := e.knownPreset(name); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	prev := st.steer.Modes().Current()
	return e.keepMode(ctx, conversation, prev, name, func() error { return st.owner.SetMode(name) })
}

// Mode is a root conversation's deliberate mode, "" when none is set.
func (e *Engine) Mode(ctx context.Context, conversation string) (string, error) {
	st, err := e.rootSteerer(ctx, conversation)
	if err != nil {
		return "", err
	}
	return st.steer.Modes().Current(), nil
}

// AcceptSuggestion applies a classifier suggestion the owner accepted: a
// preset becomes the deliberate mode (and is kept), a slot is pinned until
// the window in front changes (in memory only). view is the view in front.
func (e *Engine) AcceptSuggestion(ctx context.Context, conversation string, v steer.Verdict, view string) error {
	st, err := e.rootSteerer(ctx, conversation)
	if err != nil {
		return err
	}
	var all []steer.Slot
	if e.cfg.Steering.Slots != nil {
		if err := e.guardHost(conversation, func() error { all = e.cfg.Steering.Slots(); return nil }); err != nil {
			return err
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if v.Facet != steer.FacetPreset || v.Outcome != steer.OutcomeSuggest {
		return e.guardHost(conversation, func() error { return st.owner.Accept(v, view, all) })
	}
	if err := e.knownPreset(v.Label); err != nil {
		return err
	}
	prev := st.steer.Modes().Current()
	return e.keepMode(ctx, conversation, prev, v.Label, func() error { return st.owner.Accept(v, view, all) })
}

// guardHost runs fn, which calls into the host's steering hooks, turning a
// panic into an error.
func (e *Engine) guardHost(conversation string, fn func() error) error {
	return e.panics.Call("steering", fn, panics.With("conversation", conversation))
}

// steered is what steering adds to a turn.
type steered struct {
	updates  []slots.Update
	halt     string
	decision steer.Decision
	event    events.Steered
}

// steerKind is the kind of turn being steered.
type steerKind int

const (
	// steerPerson is a person's message: reflexes and the classifier run.
	steerPerson steerKind = iota
	// steerNotice is a turn a notice started: the mode and work slots only.
	steerNotice
	// steerResumed is an interrupted turn resumed: the mode only.
	steerResumed
)

// PreparedSteer is steering run ahead of a person's turn (Engine.Steer), so a
// host sees a reflex halt before it does anything else with the message. The
// turn sent with it in Input.Steer uses it instead of steering again.
type PreparedSteer struct {
	conversation string
	text         string
	out          *steered
	// taken is shared by every copy, so a copy cannot be used again.
	taken *atomic.Bool
}

// Halted reports whether a reflex answered the message: its turn replies
// with the reflex's message and calls no model.
func (p *PreparedSteer) Halted() bool { return p != nil && p.out != nil && p.out.halt != "" }

// take hands the prepared steering to the turn it was prepared for, once.
func (p *PreparedSteer) take(conversation, text string) (*steered, error) {
	if p.conversation != conversation || p.text != text {
		return nil, errors.New("nanite: Input.Steer was prepared for another message")
	}
	if p.taken == nil || p.taken.Swap(true) {
		return nil, errors.New("nanite: Input.Steer was already used by a turn")
	}
	return p.out, nil
}

// Steer runs steering for a person's message ahead of its turn: the mode,
// work slots, classifier and reflexes, firing what fires (a reflex's
// cooldown starts now, and stays started if the turn then fails to send).
// Send the turn with the result in Input.Steer.
func (e *Engine) Steer(ctx context.Context, conversation string, input Input) (*PreparedSteer, error) {
	if e.cfg.Steering == nil {
		return nil, ErrNoSteering
	}
	if input.Source != "" {
		return nil, errors.New("nanite: only a person's message is steered ahead of its turn")
	}
	info, err := e.cfg.Store.Conversation(ctx, conversation)
	if err != nil {
		return nil, err
	}
	if info.ParentID != "" {
		return nil, errors.New("nanite: only a root conversation is steered")
	}
	out, err := e.steerTurn(ctx, info, input, steerPerson)
	if err != nil {
		return nil, err
	}
	return &PreparedSteer{conversation: conversation, text: input.Text, out: out, taken: new(atomic.Bool)}, nil
}

// priorSignals is how many earlier messages predicate reflexes see.
const priorSignals = 8

// steerTurn runs steering for a root turn. A person's message gets the
// classifier and the reflexes; a notice turn gets the mode in force and the
// work slots; a resumed turn the mode only. A panic in the host's hooks (its
// slots, reflexes, selector, state fill) leaves the turn unsteered, with a
// warning: the result is nil.
func (e *Engine) steerTurn(ctx context.Context, info store.Conversation, input Input, kind steerKind) (*steered, error) {
	var out *steered
	err := e.guardHost(info.ID, func() (err error) {
		out, err = e.steerWith(ctx, info, input, kind)
		return err
	})
	if _, ok := panics.Ref(err); ok {
		e.cfg.Logger.WarnContext(ctx, "steering failed; the turn runs without it", "conversation", info.ID, "err", err.Error())
		return nil, nil
	}
	return out, err
}

func (e *Engine) steerWith(ctx context.Context, info store.Conversation, input Input, kind steerKind) (*steered, error) {
	cfg := e.cfg.Steering
	st, err := e.steerer(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	var sit steer.Situation
	if input.Situation != nil {
		sit = *input.Situation
	}
	sit.Input = input.Text
	sit.Source = "user"
	if input.Source != "" {
		sit.Source = input.Source
	}
	person := kind == steerPerson
	var all []steer.Slot
	if cfg.Slots != nil && kind != steerResumed {
		all = cfg.Slots()
	}
	in := steer.Input{Sit: sit, Slots: all}
	if person {
		if cfg.Reflexes != nil {
			vocab := cfg.Vocabulary
			if vocab == nil {
				vocab = steer.NewVocabulary()
			}
			for _, rx := range cfg.Reflexes() {
				if err := rx.Validate(vocab); err != nil {
					e.cfg.Logger.WarnContext(ctx, "a reflex was skipped", "err", e.scrub.Text(err.Error()))
					continue
				}
				in.Reflexes = append(in.Reflexes, rx)
			}
		}
		if prior, err := e.cfg.Store.LastMessages(ctx, info.ID, priorSignals); err == nil {
			for i := len(prior) - 1; i >= 0; i-- {
				in.Prior = append(in.Prior, signal(prior[i]))
			}
		}
		if cfg.Classify != nil {
			cctx, cancel := context.WithTimeout(ctx, classifyTimeout)
			err := e.panics.Call("classifier", func() (err error) {
				in.Classified, err = cfg.Classify(cctx, sit, all)
				return err
			}, panics.With("conversation", info.ID))
			cancel()
			if err != nil {
				in.Classified = nil
				e.cfg.Logger.WarnContext(ctx, "the message was not classified", "err", e.scrub.Text(err.Error()))
			}
		}
	} else {
		// Keep the preset the last person's message was classified into.
		st.mu.Lock()
		if st.last != nil {
			in.Classified = steer.Classification{steer.FacetPreset: *st.last}
		}
		st.mu.Unlock()
	}
	turn, err := st.steer.Prepare(ctx, in)
	if err != nil {
		e.cfg.Logger.WarnContext(ctx, "a reflex could not be evaluated or fired", "err", e.scrub.Text(err.Error()))
	}
	if person {
		st.mu.Lock()
		st.last = nil
		if turn.Mode.Source == steer.SourceClassified {
			r := in.Classified[steer.FacetPreset]
			st.last = &r
		}
		st.mu.Unlock()
	} else {
		// Nothing was classified and no reflex layer ran for this turn.
		turn.Decision.Classified, turn.Decision.Verdicts, turn.Decision.Fired = nil, nil, nil
		turn.Suggestions, turn.Fired = nil, steer.Fired{}
	}
	if len(turn.Fired.Names) > 0 {
		// Keep the cooldowns, so a restart does not fire these again too soon.
		if raw, err := json.Marshal(st.cooldowns.Snapshot()); err == nil {
			if err := e.cfg.Store.SetSetting(ctx, info.ID, settingCooldowns, string(raw)); err != nil {
				e.cfg.Logger.WarnContext(ctx, "reflex cooldowns were not kept", "err", e.scrub.Text(err.Error()))
			}
		}
	}
	out := &steered{halt: turn.Fired.Halt, decision: turn.Decision}
	if text := modeText(turn.Mode.Preset); text != "" {
		out.updates = append(out.updates, slots.Update{Slot: slots.Mode, Entry: slots.Entry{Owner: "steer", Key: "preset", Content: text}})
	}
	if kind != steerResumed {
		if text := workText(turn); text != "" {
			out.updates = append(out.updates, slots.Update{Slot: slots.UserContext, Entry: slots.Entry{Owner: "steer", Key: "work", Content: text}})
		}
	}
	out.event = events.Steered{Mode: turn.Mode.Preset.Name, ModeSource: turn.Mode.Source, Slots: turn.Selection.Names(),
		Why: turn.Selection.Why, Fired: turn.Fired.Names, Halted: out.halt != "", Suggestions: turn.Suggestions, Actions: turn.Fired.Actions}
	if kind == steerResumed {
		out.event.Slots, out.event.Why = nil, ""
		out.decision.Slots, out.decision.Why = nil, ""
	}
	return out, nil
}

// modeText is the preset in force, as the agent is told it in the mode slot.
// The default preset with nothing to say adds nothing.
func modeText(p steer.Preset) string {
	var parts []string
	if t := strings.TrimSpace(p.Instructions); t != "" {
		parts = append(parts, t)
	}
	if p.OutputStyle != "" {
		parts = append(parts, "Output style: "+p.OutputStyle+".")
	}
	if len(parts) == 0 {
		return ""
	}
	return "Mode: " + p.Name + ". " + strings.Join(parts, " ")
}

// workText is what the agent is told with the message: the active work slots,
// the tools top of mind, and reminders the reflexes fired.
func workText(t steer.Turn) string {
	var b strings.Builder
	for i, sl := range t.Selection.Slots {
		if sl.Name == "" {
			continue
		}
		if i == 0 {
			b.WriteString("Work in front: ")
		} else {
			b.WriteString("Also active: ")
		}
		b.WriteString(sl.Name)
		if sl.About != "" {
			b.WriteString(" (" + sl.About + ")")
		}
		b.WriteString(".")
		if t := strings.TrimSpace(sl.Instructions); t != "" {
			b.WriteString(" " + t)
		}
		if sl.Where != "" {
			b.WriteString(" Kept in: " + sl.Where + ".")
		}
		b.WriteString("\n")
	}
	if len(t.Tools) > 0 {
		b.WriteString("Top of mind: " + strings.Join(t.Tools, ", ") + ".\n")
	}
	if len(t.Fired.Reminders) > 0 {
		b.WriteString("Reminders:\n")
		for _, r := range t.Fired.Reminders {
			b.WriteString("- " + r + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// signal is a stored message as predicate reflexes see it.
func signal(m store.Message) reflexes.MessageSignal {
	sig := reflexes.MessageSignal{MessageID: m.ID, Role: m.Role, CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z07:00")}
	var text []string
	for _, b := range m.Blocks {
		switch b.Type {
		case "text":
			if b.Name == "" {
				text = append(text, b.Text)
			}
		case "tool_use":
			sig.ToolCalls++
			sig.ToolNames = append(sig.ToolNames, b.Name)
		}
	}
	sig.Content = strings.Join(text, "\n")
	return sig
}
