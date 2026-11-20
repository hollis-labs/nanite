# Steering

Steering decides how the agent approaches a turn before the model sees it:
which mode is in force, which kinds of work are in front, which reflexes fire,
and what a classifier may suggest. Nanite runs it with the
[`agent/steer`](https://github.com/hollis-labs/libs/tree/main/agent/steer)
library. A host turns it on with `agent.Options.Steering`; without it, turns
are assembled exactly as before.

```go
reg, _ := steer.NewRegistry(steer.DefaultPresets()...)  // general, focus, triage, research
_ = reg.Add(steer.LayerModule, "drafts", steer.Preset{Name: "writing", Slots: []string{"drafts"}, Instructions: "Help the owner write."})
a, _ := agent.Open(ctx, agent.Options{Dir: dir, Steering: &agent.Steering{
	Registry: reg,
	Slots:    func() []steer.Slot { return mySlots },       // kinds of work the host offers
	Reflexes: func() []steer.Reflex { return myReflexes },  // standing rules, as config
	Classify: myClassifier,                                  // optional
}})
c, _ := a.Conversation(ctx, id)
_ = c.SetMode(ctx, "writing") // the person picked a mode in the host's UI
run, _ := c.Send(ctx, agent.Input{Text: msg, Situation: &steer.Situation{Focused: &steer.Focus{View: "drafts"}}})
```

## What a root turn gets

Steering runs for root conversations only; a subtask works under its variant.

| Turn | Mode | Work slots | Classifier | Reflexes |
| --- | --- | --- | --- | --- |
| A person's message | yes | yes | yes, if `Classify` is set | yes |
| A notice turn | yes | yes | no | no |
| A resumed turn | yes | no | no | no |

Subtasks and `Dispatch` do not inherit the mode: a subtask conversation has
none, and its turns are not steered. A notice or resumed turn keeps a preset
the classifier applied (`auto`) on the person's last message, as long as the
policy still applies it; it is not classified again.

- **Mode.** The preset in force: the conversation's deliberate mode, otherwise
  a classified preset the policy applies, otherwise the registry's default. Its
  instructions and output style go in the `mode` slot, in the system blocks;
  they change only when the mode changes.
- **Work slots.** The kinds of work in front (from the window in front, the
  mode and the preset), with their instructions and the tools top of mind. They
  go in the `user_context` slot, in front of the person's message, so the
  system blocks and the prompt cache stay the same as focus moves.
- **Reflexes.** Those enabled everywhere plus those the preset lists. A
  `remind` reminder is told with the message. A `halt` answers the message
  itself: the reply is the reflex's message and no model is called. Any other
  action kind in the vocabulary is the host's: it arrives in the `Steered`
  event's `Actions`. An invalid reflex, or one whose action kind is not in the
  vocabulary, is skipped with a warning. Cooldowns are kept per conversation,
  so a restart does not fire a cooling reflex again. Predicates see the
  situation, `Situation.Signals`, and whatever `Steering.StateFill` adds
  (unread mail, prefix tokens, scope tier).

The host's hooks are guarded: a panic in `Slots`, `Reflexes`, `NewSelector`,
the selector or `StateFill` leaves that turn unsteered, with a warning, and
the turn still runs. The classifier gets 15 seconds per message; past that,
or on an error, the message is not classified.

Each root turn emits a `Steered` event before the model runs (chatrunner
sends it as the `nanite.steer` activity). The turn's trace record carries the
decision in its `situation`, `classified`, `why` and `fired` fields, with no
prompt content: the views and module in front, the mode and its source, the
layout, the turn source and the slots, but no message text, selection, window
titles or signals. `fired` is null on a notice or resumed turn, where no
reflex layer runs. A turn a reflex halted has `why` starting with `halted`,
no provider or model, and no steps.

## Steering ahead of the turn

A host that acts on a message before its turn (chatrunner's front desk does)
steers it first with `Conversation.Steer`. If the result is `Halted`, a reflex
answered the message and nothing else should act on it. Send the turn with
the result in `Input.Steer`: the turn uses it instead of steering again, so a
reflex fires once. It is for that conversation and text only, and used once
(a copy of it too). The reflexes fire in `Steer`: if the `Send` that follows
fails, their cooldowns have still started.

chatrunner's `Options.OnSteered` runs inline with the turn's wire events, so
keep it fast and hand slow work off.

## Modes

- **Only the person sets the mode**, through `Conversation.SetMode`. There is
  no model tool for it. Nanite holds the library's owner-control capability
  internally and never exposes it to the model. The mode is kept across
  restarts, and `""` clears it.
- The default preset is not a mode: it does not select slots that list a mode,
  and `mode` attributes stay empty for it.
- A deliberate mode beats a classified one.
- `Conversation.AcceptSuggestion` applies a suggestion the person accepted: a
  preset becomes the deliberate mode, a work slot is pinned until the window in
  front changes. Only a suggestion for a known preset or offered slot is
  accepted.
- The mode and reflex cooldowns are kept in the store (`conversation_settings`),
  which rebuilding the index from the transcripts leaves alone. A slot pin is
  kept in memory only: a restart, or a conversation idle for 30 minutes, drops
  it. Owner changes to the mode are written to the store before they apply.

**Layers.** Presets come from the shipped defaults, from modules, and from the
app. A preset of the same name in a higher layer replaces a lower one whole,
and **the app wins over modules, which win over the defaults**
(app > module > default). Between two modules, the one registered first wins.

## Classifier

The host classifies; Nanite never sends the message anywhere to do it. Per
facet the policy is `shadow` (record only), `suggest` (offer to the person
above a threshold) or `auto` (apply above a threshold). The default suggests
`slot` at 0.7 and `preset` at 0.8, and nothing applies on its own. Set a facet
to `auto` only once replay shows enough precision.

In this version the `act` and `affect` facets are **recorded as data only**:
they appear in the trace and are never suggested or applied.
