# plugin-staging

This directory holds **runtime state** — a temporary staging area used during
plugin installation. Nanite extracts a plugin here, validates it, then
atomically promotes it into `plugin-data/`. Any leftover contents indicate
an interrupted install and are safe to remove.

It is not managed by `nanite-agent init`; contents here are user-ephemeral.

Do not edit files in here expecting them to persist across runs or installs.
Delete at will; Nanite will repopulate as needed.
