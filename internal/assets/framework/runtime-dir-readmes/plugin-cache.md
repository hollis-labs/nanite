# plugin-cache

This directory holds **runtime state** — downloaded plugin archives cached by
the Nanite plugin manager to avoid redundant network fetches.

It is not managed by `nanite-agent init`; contents here are user-ephemeral.

Do not edit files in here expecting them to persist across runs or installs.
Delete at will; Nanite will repopulate as needed.
