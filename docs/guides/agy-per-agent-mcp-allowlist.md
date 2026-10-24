# Per-agent MCP allowlist for headless Antigravity (agy)

Status: verified live on 2026-10-24 (agy CLI on macOS, model `gemini-3.8-flash-low`). Generic: it applies to any harness that launches `agy` headlessly (`agy -p`, `--print`) and wants an agent to call a chosen set of MCP tools and no others. Written from the Nanite implementation (CW-20261024-0035 research, CW-20261024-0109 implementation); the Nanite names in the examples are placeholders for your own.

## 1. The problem

A headless `agy` run has no one to answer a permission prompt. When the model calls an MCP tool, agy checks permissions, finds no rule, cannot ask, and **auto-denies**:

- the tool step ends `TOOL_ERROR: permission check failed for mcp "<server>/<tool>": user denied permission ...`;
- the process still **exits 0**, the final response is **empty**, and stderr says `a tool required the "mcp" permission that headless mode cannot prompt for, so it was auto-denied. Add an allow-rule under permissions.allow in settings.json`.

The usual escape, `--dangerously-skip-permissions`, approves every tool for the run. That is the opposite of a per-agent allowlist.

What the investigation established about where an allow rule can come from:

| Mechanism | Result |
| --- | --- |
| `settings.json` `permissions.allow` with `mcp(<ServerName>/<tool>)` | **Works.** Honoured in print mode. |
| A workspace file (`<workspace>/.agents/...`) carrying permissions | **Does not exist.** The only settings paths agy loads are `~/.gemini/antigravity-cli/settings.json` (user) and `/etc/antigravity/admin_settings.json` (admin). |
| A workspace `PreToolUse` hook (`<workspace>/.agents/hooks.json`) returning `decision: allow` | **Does not approve.** Hooks load and fire (payload below), a hook `deny` holds, but an `allow` is advisory: agy's own permission check still ran and denied. Do not build on hooks for allowing. |
| `--dangerously-skip-permissions` | Approves everything. Not per-tool. |

So the allowlist has to live in a `settings.json` that only this agent's agy process reads. agy resolves that file from `$HOME`, so the answer is a per-agent `HOME`.

## 2. The mechanism

Launch each agy agent with `HOME` pointing at a **farm home**: a directory of symlinks to the real home's entries, except `.gemini` (and `.agents`), which are private to the agent. The private `.gemini` carries symlinks to the operator's login files and its own generated `settings.json`.

Nanite's reference implementation (PR hollis-labs/nanite#569): `internal/runtime/agent/bootdir_agy_home.go` (farm and private `.gemini`, kept per agent under `~/.nanite/agy-gemini/<slug>/` and linked from the farm), `agy_mcp_allow.go` (rules and merge, owned rules tracked in a sidecar `nanite-owned-rules.json`), and an env-gated live test `agy_live_test.go`.

Result: the agent sees its own allowlist; the operator's real `~/.gemini` (desktop app, other agents) is never read or written by the agent's settings layer; the global MCP servers, plugins (for example a global hook plugin) and skills under the real `~/.gemini`/`~/.agents` are hidden, a side benefit.

### 2.1 Layout

```
<farm>/                         HOME for the agy process
  <every entry of the real $HOME>   -> symlink to the real entry
                                    EXCEPT: .gemini, .agents, .DS_Store, .CFUserTextEncoding, .Trash
  .gemini/                      a real directory, private to this agent
    oauth_creds.json                 -> symlink to ~/.gemini/oauth_creds.json
    google_accounts.json             -> symlink to ~/.gemini/google_accounts.json
    jetski-standalone-oauth-token    -> symlink to ~/.gemini/jetski-standalone-oauth-token
    installation_id                  -> symlink to ~/.gemini/installation_id
    antigravity-cli/
      antigravity-oauth-token        -> symlink to ~/.gemini/antigravity-cli/antigravity-oauth-token
      settings.json                  generated, 0600 (see 2.2)
    config/                          empty directory
```

Link only the auth files that exist. Everything else in the real home (`~/.ssh`, `~/dev`, `~/Library`, dotfiles) resolves normally through the farm, so git and ssh keep working.

Where to keep the private `.gemini`: per agent and persistent (not rebuilt per launch). agy's first-run onboarding and consent answers, and the conversation history, then survive across launches. The farm of symlinks itself is cheap and can be rebuilt per launch.

### 2.2 The generated `settings.json`

```json
{
  "permissions": {
    "allow": [
      "mcp(tether_nanite/agent_list)",
      "mcp(tether_mydb/query)"
    ]
  }
}
```

- One rule per granted tool: `mcp(<ServerName>/<tool>)`. Prefer explicit per-tool rules. The server-scoped wildcard `mcp(<ServerName>/*)` was tested once (agy 1.4.7) and works, so use it only for a server whose tool set you control; other wildcard forms (`mcp(*)`) are untested and agy's own docs warn against them.
- **Merge, do not clobber.** The file may already hold keys agy or the operator wrote (`model`, `trustedWorkspaces`, onboarding state). Keep unrelated keys. Regenerate only the rules you own, and drop stale ones, so a revoked grant disappears at the next launch (track ownership with a marker, for example a sidecar file listing the rules you wrote).
- Mode 0600.
- Whether `trustedWorkspaces` is needed for headless runs: the verification ran in an untrusted throwaway workspace and the allow rule was honoured, so it was not needed.

### 2.3 How `<ServerName>` is derived

agy registers a workspace plugin's MCP servers as `<plugin>_<server>`:

- plugin = the plugin under `<workspace>/.agents/plugins/<plugin>/` (its `plugin.json` `{"name":"<plugin>"}`);
- server = a key of `mcpServers` in `<workspace>/.agents/plugins/<plugin>/mcp_config.json` (`{"mcpServers":{"<server>":{"command":...,"args":[...],"env":{...}}}}`).

Verified: plugin `stub`, server `stub` appeared as `ServerName: "stub_stub"` in agy's own tool-call record and in the hook payload. A harness that always names its plugin `tether` and registers servers `nanite` and `mydb` should write `mcp(tether_nanite/<tool>)` and `mcp(tether_mydb/<tool>)`. Nanite's implementation confirmed it live: host server `stub` -> `tether_stub`, Nanite's own server -> `tether_nanite`. Confirm the exact string on your first live turn: it is in the stream-json event `step_update.tool_info.parameters.ServerName`.

Tool names are the server's own tool names (`ok_tool`), not prefixed.

### 2.4 Server discovery

agy sends a `server/discover` JSON-RPC request first. Your MCP server must answer unknown methods with a JSON-RPC error (`-32601`) or an HTTP 4xx; a body-less `202` reply breaks the handshake. After that it sends the usual `initialize`, `notifications/initialized`, `tools/list`.

## 3. Evidence

Setup common to both turns: throwaway workspace, a stub stdio MCP server exposing `ok_tool` and `no_tool` (each returns a fixed string and logs its `tools/call`), plugin `stub` with `mcp_config.json`, `agy --model gemini-3.8-flash-low --print-timeout 150s --output-format stream-json -p 'Call the MCP tool ok_tool from the stub server, then call the MCP tool no_tool from the stub server. Use no other tools. Reply with one line per tool: the tool name and either its result text or the denial message.'`, no `--dangerously-skip-permissions`, `permission_mode: request-review`.

| # | Setup | Result | Tokens |
| --- | --- | --- | --- |
| 1 | Workspace hook that denies unless the tool is `ok_tool` (my first gate also matched the prompt text and denied both; fixed) | A hook **deny** holds: `tool call denied by pre-tool hook: <reason>`; stub never called | ~29.8k |
| 2 | Same hook with a correct gate, `decision: allow` for `ok_tool` | Still `permission check failed ... user denied permission`: hook allow does **not** approve; stub never called | ~9.8k |
| 3 | **Farm HOME, private `.gemini/antigravity-cli/settings.json` with `permissions.allow: ["mcp(stub_stub/ok_tool)"]`, no hook** | `ok_tool` **executed** (stub logged `tools/call`); `no_tool` **denied** with `permission check failed ... user denied permission for mcp(stub_stub/no_tool)`. Per-tool granularity inside one server proven | 15.7k |
| 4 | Same as 3, with the operator's global hook plugin (`tether-pulse`, `PreToolUse` matcher `.*`) copied into the private `.gemini/config/plugins/` so it is active | Identical to 3: no interference | 16.1k |

(Token counts are agy's `usage.total_tokens`. Turn 3 and 4 numbers include the reply turns.)

The hook payload, for reference (workspace hooks are not part of the recommended mechanism): `toolCall.name` is always `call_mcp_tool`, `toolCall.args` = `{ServerName, ToolName, Arguments}`, plus `conversationId`, `stepIdx`, `lastUserInput`, `workspacePaths`, `modelName`. Do not match on the whole stdin: it contains the prompt.

| 5 | Nanite's real launch path (HOME from the agy layout's AmendEnv, generated settings, two servers: host `stub` granted only `ok_tool` via explicit rule, Nanite's own server via `mcp(tether_nanite/*)`), asked `ok_tool`, `own_tool`, `no_tool` | `ok_tool` ran, `own_tool` ran via the wildcard, `no_tool` denied; ServerNames `tether_stub` and `tether_nanite`; exit 0, empty response, stderr hint. A before/after listing of the real `~/.gemini` and `~/.agents` showed no change | 24.4k (an earlier turn on the same path: 16.1k, denial ended it before the second server was reached) |

Note on ordering: a denial can end the turn, so put the tool you expect to be denied last when a run must reach several allowed tools.

## 4. Implementation checklist

1. Decide the per-agent private `.gemini` location (persistent, one per agent).
2. At launch, build the farm home (2.1): symlink every real-home entry except the excluded set; ensure the private `.gemini` exists; link the existing auth files.
3. Compute the allow rules from the agent's grants: for each (plugin, server, granted tool) emit `mcp(<plugin>_<server>/<tool>)`.
4. Merge them into `<private .gemini>/antigravity-cli/settings.json` (2.2), 0600, removing rules you own that are no longer granted.
5. Plant the workspace plugin: `<workspace>/.agents/plugins/<plugin>/plugin.json` and `mcp_config.json` with the same server names you used in step 3.
6. Spawn agy with `HOME=<farm>` in its environment (and the usual `--add-dir`, `--model`, `-p` arguments). Do not pass `--dangerously-skip-permissions`.
7. Treat "exit 0, empty response, stderr says auto-denied" as a normal denied-tool outcome in your supervisor, not a crash.
8. Keep the old refusal for anything you cannot gate (hooks, non-MCP tools, see section 6).

## 5. Test plan

- **Golden tests (no model, no network):** given grants, the generated `settings.json` equals the expected rules; merge keeps unrelated keys; a revoked grant removes its rule; file mode 0600; the farm contains symlinks for every real-home entry except `.gemini`/`.agents`; `HOME` is set in the spawn env; with `HOME` set to a temp dir, nothing is written under the real `~/.gemini`.
- **One live stub turn (about 10k tokens, cheapest flash model), per agy upgrade and on first implementation:**
  1. Write a stdio MCP stub with two tools (`ok_tool`, `no_tool`); answer `server/discover` and other unknown methods with a JSON-RPC error; append every `tools/call` name to a log file.
  2. Plant it through your real launch path as a granted server where only `ok_tool` is granted.
  3. Run the `agy -p ... --output-format stream-json` command above.
  4. Pass criteria: the stub log shows `ok_tool` and not `no_tool`; the stream shows `ok_tool` DONE and `no_tool` ERROR with `user denied permission`; record `ServerName` from the stream and check it equals your computed prefix.
  5. Afterwards confirm nothing under the real `~/.gemini` changed apart from what agy itself writes (conversation state, logs).
- Run this live test only on demand (env-gated), never in default CI: it spends model quota.

## 6. Risks and limits

- **Version coupling.** The rule syntax `mcp(<server>/<tool>)`, the settings path, the `<plugin>_<server>` naming and the print-mode behaviour are observed (binary strings, changelog, live runs), not a documented API. Keep the live stub turn as a smoke test and re-run it on every agy update.
- **Shared login through symlinks.** The auth files in the private `.gemini` are symlinks to the real ones, so a token refresh by the agent writes through to the operator's real files, and the desktop app and agents share one login. Same trade-off as any farm home. Do not copy tokens instead (they would go stale).
- **Wildcards.** Only `mcp(<ServerName>/*)` was verified (one live run on agy 1.4.7). Everything else is untested; prefer per-tool rules for third-party servers.
- **Non-MCP tools stay denied.** `run_command`, file writes and `read_url` need their own rules (`command(<prefix>)`, `write_file(<path>)`, `read_file(<path>)`, `read_url(<domain>)`) and are denied headless without them. Do not silently allow them. Define an explicit per-agent policy; never wildcard.
- **macOS only verified.** Symlink semantics and the auth-file set may differ elsewhere.
- **Files written at `~` land in the farm,** not the real home, if the agent writes outside the symlinked entries.
- **Empty exit-0 after denial** (section 1): supervisors must not read it as success or as a crash.
- Hook-based gating is out: `allow` is advisory.

## 7. What not to do

- Do not rely on a workspace hook to allow tools.
- Do not edit the real `~/.gemini/antigravity-cli/settings.json` for a per-agent allowlist: it is global to the user and shared with the desktop app.
- Do not use `--dangerously-skip-permissions` to get MCP working.
- Do not use wildcards (`mcp(*)`, `command(*)`).
- Do not copy auth tokens into the farm.
- Do not match hook gates on the whole stdin.

## 8. Rollback

The farm and private `.gemini` are additive and live outside the real `~/.gemini`. To roll back: stop setting `HOME` for agy launches (agy returns to the real home and its shared global config), remove the farm directories and the per-agent private `.gemini`, and restore the previous refusal for agy MCP grants so nothing runs ungated. The real `~/.gemini` was never edited, so there is nothing to restore there.
