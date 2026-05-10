# plugin-catalog

This directory holds **runtime state** — downloaded catalog manifest files
fetched by the Nanite plugin manager when resolving available plugins.
Manifests are refreshed automatically on demand.

It is not managed by `nanite-agent init`; contents here are user-ephemeral.

Do not edit files in here expecting them to persist across runs or installs.
Delete at will; Nanite will repopulate as needed.
