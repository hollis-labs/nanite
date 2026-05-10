# tool-output

This directory holds **runtime state** — cached tool output blobs written by
the Nanite chat app during a session (Phase 3 S4a cache-and-pointer pattern).
Entries are keyed by a content-addressed ID and referenced from session state.

It is not managed by `nanite-agent init`; contents here are user-ephemeral.

Do not edit files in here expecting them to persist across runs or installs.
Delete at will; Nanite will repopulate as needed.
