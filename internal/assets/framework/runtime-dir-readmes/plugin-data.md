# plugin-data

This directory holds **runtime state** — installed plugin data written by the
Nanite plugin manager when a plugin is activated. Each plugin subdirectory is
owned by that plugin and may contain databases, indexes, or other working files.

It is not managed by `nanite-agent init`; contents here are user-ephemeral.

Do not edit files in here expecting them to persist across runs or installs.
Delete at will; Nanite will repopulate as needed.
