# Nanite architecture

Nanite runs one authored agent directory per process. The embedding API lives
in `agent`. Substrate handles CLI startup in `cmd/nanite`. Each turn reads the
agent definition and takes a snapshot of the host's tool groups and context
contributions.

## Conversation storage

The agent's data directory defaults to `<agent dir>/.nanite/`. Each
conversation has a transcript under `transcripts/<conversation ID>/`. The
directory name is the conversation's ULID. Numbered JSONL segments record
messages and turn state transitions.

Nanite syncs each transcript record before updating SQLite. It commits the
projection and transcript cursor in the same transaction, then replays any
remaining records on open. New schema changes use numbered migrations.
