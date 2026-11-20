# Turn profiles

A host can compose a seat and module into one directory per profile, once,
then select that stable directory for each turn. It no longer needs to rewrite
the agent's Open directory when the active module changes.

```go
run, err := conversation.Send(ctx, agent.Input{
    Text: "Review the change",
    AgentDir: reviewProfileDir,
    Headers: map[string]string{"X-App-Profile": "review"},
})
```

`AgentDir` is a composed profile, not a sparse overlay. It requires `agent.md`
and may contain `context/`, `skills/`, `variants/`, `tools/` and `steering.json`.
`README.md` and `evals/` are ignored. Files must be regular, without symlinks,
at most 1 MiB each and 4 MiB total. The same agentdir and authored validators
used for agent directories validate profiles. Validation results are cached
by absolute path and content/mode digest, with a bounded cache.

Profiles cannot contain `host.yaml`, `providers.yaml`, `identity.yaml`,
`permissions.yaml` or `.mcp.json`. These files, the working directory,
authentication, providers, permissions and MCP configuration remain the host's.
For profile turns their configuration is retained from Open. Invalid profiles
or headers fail before a run is registered or any conversation history changes.
A turn without `AgentDir` uses the existing agent-directory behavior.

The root turn and all its descendants retain the parsed profile snapshot.
Switching profiles in a later turn cannot change an existing background lead's
variants, including variants used for children it starts after the switch.
Keep profile directories at their stable paths while work uses them: executable
tool packages still run from those paths and are rechecked before execution.
Package approval remains keyed by package name and digest; the same named,
identical package in two profile directories shares its approval. Per-turn tool
packages cannot request environment passthrough (`env`) or Warden tokens
(`needs`); validation refuses either. Skill bodies come from the validated
snapshot. Skill directory paths still point to the stable profile directory,
so auxiliary files referenced by a skill must remain immutable there.

## Steering

`steering.json` uses the existing presets/reflexes format:

```json
{
  "presets": [{"name": "careful", "reflexes": ["check-first"]}],
  "reflexes": [{
    "name": "check-first",
    "trigger_kind": "text",
    "trigger": {"pattern": "(?i)ship it"},
    "action_kind": "remind",
    "action": {"body": "Check before shipping."}
  }]
}
```

The optional `workbench_format` field is accepted. Unknown fields, duplicate
presets/reflexes, invalid reflexes and missing reflex references are errors.
Host steering callbacks, classifier policy, vocabulary and work slots remain
in effect. Profile presets use the module layer: they override default presets, while
explicit app-layer presets win. The host's default preset name is retained. Without host steering, the first profile preset is the
default. A profile without `steering.json` uses host steering.

The conversation's deliberate mode and selector (including pinned work slots)
survive switches. A mode absent from the selected registry falls back for that
profile; its persisted choice is retained for a profile that defines it again.
Prepared steering is bound to the selected directory and digest as well as the
message, and may be consumed only once.

## Headers and recovery

Headers are copied before the run begins and apply to MCP HTTP/SSE calls
and the built-in model gateway adapters, including descendants. They do not
modify shared clients. Connection streams and close requests (GET/DELETE)
never receive turn headers. No fixed profile header name is introduced: the example
uses an application-owned name, which applications should coordinate with
receiving services. Custom provider implementations receive the request context
but do not automatically gain an HTTP transport implementation.

Authorization, cookies, host, content/connection framing, proxy/forwarding
headers, and headers owned by the Warden, MCP or model adapters are refused.
Names containing auth, token, key, secret, cookie, session, password or credential
(case-insensitive) are also refused. There may be at most 16 headers, each
value at most 256 bytes of printable ASCII. Values that the redactor would
change are refused before the turn is stored; errors name only the header.
Use this API only for non-secret correlation metadata: accepted header values
are persisted with the turn. Host-configured headers always win over a turn
header with the same name, with one warning per shadowed name per scope.
Existing authenticated transport settings retain control of credentials and identity.

The transcript and its SQLite projection store the absolute `AgentDir` and
headers when the turn starts. Recovery loads that directory, even if the host
now selects a different profile. A missing or invalid directory fails recovery
explicitly; it never falls back to the Open directory. Keep profile versions
immutable if exact content must survive a process restart: recovery reloads
the stored path, rather than persisting executable files or parsed callbacks.
A persisted empty directory or absent headers explicitly restores the Open
profile or no headers; recovery never inherits the caller’s profile context.
A new turn uses the selection supplied by the host.

## chatrunner and planning

```go
opts.TurnProfile = func(ctx context.Context, turn chat.Turn) (string, map[string]string, error) {
    profile := chooseProfile(turn)
    return profile.Dir, map[string]string{"X-App-Profile": profile.ID}, nil
}
```

The hook runs before steering, front-desk planning, dispatch and Send. A hook
error refuses the turn. `RecoverTurns` uses persisted settings without calling
the current hook. Nil keeps existing behavior.

An embedder that plans or dispatches before Send can share the same validated
snapshot explicitly:

```go
turnCtx, err := a.TurnContext(ctx, input)
// Check err, then use turnCtx for a.Complete, conversation.Dispatch and Send.
```

A turn context belongs to one agent and one selected profile; use a fresh
parent context for a later profile selection. Background work retains its
original context when the parent turn ends.
