# Running the Mac (dev) Nanite under launchd

Status: design and files only (CW-20261024-0113). Nothing is installed or loaded by this change. The cut-over needs the operator's confirmation because it touches the login session.

## Why

The Mac Nanite (workspace `mesh-chat`, `127.0.0.1:8090`, data `~/.local/share/nanite`) was started by hand from a Claude session shell. That gives it:

- no supervisor: a crash or a reboot leaves it down;
- a stale environment inherited from that shell (session tokens and variables that mean nothing to a service);
- a deploy that depends on whoever is at the keyboard, which let the database fall five migrations behind (goose 179 against 184) before the 2026-10-24 update.

It is also the instance that launches Mac-side agents, so a stable, reproducible process matters.

## Decision: a plain launchd user agent

- A user LaunchAgent in the `gui/<uid>` domain, label `com.hollis-labs.nanite.mac`. It runs in the login session, so the login keychain (`keychain://nanite/provider/...` provider keys), `SSH_AUTH_SOCK` and the user's CLIs (claude, codex, agy, opencode) resolve as they do in a terminal.
- Not a Cerberus resource for now. A Cerberus-labelled plist for `nanite-api-service` already sits in `~/Library/LaunchAgents` (not loaded, pointing at the `default` workspace and a binary under `~/.cerberus/`). It is stale and would fight for port 8090 if loaded. It is left alone; reconciling it belongs with the Cerberus state task (CW-20261024-0099). If Cerberus later manages Mac processes, the same plist is the unit to register.

## Layout

| Thing | Path |
| --- | --- |
| Plist template | `scripts/mac-nanite/com.hollis-labs.nanite.mac.plist.tmpl` |
| Helper | `scripts/mac-nanite/mac-nanite.sh` |
| Stable binary path | `~/.local/bin/nanite`, a symlink to `~/.local/share/nanite/bin/nanite-<version>` |
| Previous binary record | `~/.local/share/nanite/PREVIOUS_BINARY` |
| Backups | `~/.local/share/nanite/backups/<timestamp>-pre-<version>/` (0700; `auth.db`, `main.db`) |
| Logs | `~/Library/Logs/nanite/stdout.log`, `stderr.log` |
| Installed plist | `~/.local/share/nanite/launchd/` (manual start) or `~/Library/LaunchAgents/` (autostart) |

The service runs `~/.local/bin/nanite serve` in `~/dev/hollis-labs/apps/nanite` (the directory the hand-started process used) with an explicit environment: `HOME`, `USER`, `LOGNAME`, a fixed `PATH`, and `NANITE_WORKSPACE=mesh-chat`. No session variables are inherited. Override with `NANITE_MAC_WORKSPACE`, `NANITE_MAC_PORT`, `NANITE_MAC_WORKDIR`, `NANITE_MAC_PATH`.

## Auto-start at login: the operator's decision, default OFF

launchd loads every plist in `~/Library/LaunchAgents` at login. So the choice is where the plist lives:

- `install` (default): the plist is kept in `~/.local/share/nanite/launchd/` and loaded with `launchctl bootstrap`. It survives crashes while the login session lasts (`KeepAlive`), but after a reboot or logout nothing starts it until `mac-nanite.sh start`.
- `install --autostart`: the plist goes into `~/Library/LaunchAgents/` and the service starts at every login and is restarted after crashes. This is the setting that gives reboot recovery, and it is a login item, so it is opt-in.

Switching later is `uninstall` then `install [--autostart]`.

## Crash recovery and logs

- `KeepAlive: {SuccessfulExit: false}` restarts after a crash or a signal, not after a clean exit; `ThrottleInterval` 10 s stops a crash loop from spinning. `stop` uses `launchctl bootout`, which sends SIGTERM and unloads the job, so a deliberate stop stays stopped.
- launchd does not rotate logs. `mac-nanite.sh rotate` copy-truncates anything over 20 MB and keeps five generations; `deploy` runs it. For unattended rotation add the helper to a periodic job later; not needed yet.

## Deploy and rollback: one command each

```
scripts/mac-nanite/mac-nanite.sh deploy /path/to/nanite-darwin-arm64      # build it first, see CONTRIBUTING / the Makefile
scripts/mac-nanite/mac-nanite.sh rollback
```

`deploy`: reads the new binary's version; refuses if any agent runtime is `launching`/`running` (override with `--force`); takes a sqlite `.backup` of `auth.db` and the workspace `main.db` with an integrity check; copies the binary to `bin/nanite-<version>`; records the previous binary; flips the `~/.local/bin/nanite` symlink; rotates logs; `launchctl kickstart -k`; waits for `/api/health` to report `ok` and the new version; on failure points the symlink back and restarts the old binary. It keeps the last three binaries.

A migration is not safely reversible once the database has been written to. When the new binary's migrations ran, a rollback of the binary alone can leave an older binary on a newer schema. The failure message prints the backup directory; restore the two database copies with the service stopped (`mac-nanite.sh stop`, copy over, delete `-wal`/`-shm`, `start`).

## Cut-over plan (after the operator confirms)

1. Build the binary for the current `main` (darwin/arm64) as for any deploy.
2. `mac-nanite.sh deploy <binary>`: installs the versioned binary and the `~/.local/bin/nanite` symlink; the service is not loaded yet, so nothing restarts.
3. Stop the hand-started `./nanite*.serve` process cleanly (SIGTERM) after checking that no sessions or agent runtimes are active.
4. `mac-nanite.sh install` (or `install --autostart`).
5. Verify: `/api/health` version, `nanite agent list`, 401 on unauthenticated routes, log files filling, then `launchctl kill SIGKILL gui/$(id -u)/com.hollis-labs.nanite.mac` and confirm it comes back (crash recovery).
6. Leave the old hand-started binary where it is as the rollback.

## Risks and unknowns

- **launchd environment differences.** The service gets only the variables in the plist. A CLI that needs another variable from the operator's shell (for example a token exported in `.zshrc`) will not see it. The fixed `PATH` was taken from the process that ran until now; check each agent runtime once after cut-over.
- **Keychain access.** A LaunchAgent in the GUI domain can read the login keychain, but the first access after a reboot may prompt while the screen is locked.
- **Port.** `install` refuses to load while a hand-started `nanite serve` holds the port.
- **Working directory.** `WorkingDirectory` points at the repository checkout, as the hand-started process did; a service should not depend on a source tree long term, but changing it can change relative-path behaviour of agent launches, so it is not changed here.
- **macOS only.** agent-os already runs under a systemd user unit.

## Not covered

Autostart policy (operator's decision), unattended log rotation, Cerberus registration, removal of the stale `com.fragments-engine.cerberus.nanite.nanite-api-service.plist`.
