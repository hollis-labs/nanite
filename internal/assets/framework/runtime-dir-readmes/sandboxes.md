# sandboxes

This directory holds **runtime state** — per-session sandbox working directories
created by the Nanite chat app at session start and cleaned up when the session ends.

It is not managed by `nanite-agent init`; contents here are user-ephemeral.

Do not edit files in here expecting them to persist across runs or installs.
Delete at will; Nanite will repopulate as needed.
