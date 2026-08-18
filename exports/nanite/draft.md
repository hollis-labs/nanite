---
path: draft.md
type: note
title: ::draft
description: ::draft  Loom Wiki Pilot eighth end-to-end smoke test, after restarting the stale mux MCP proxy on Nanite's side. This fragment tests whether Curator can now fetch fragment content...
status: active
generated_by: loom-compiler
generated_at: "2026-08-17T22:30:27.879697Z"
content_hash: 69befd590824045bca6cc23ddc19bb6c8bd5d01640f5bbfd8e941e167201e688
---

# ::draft

::draft

Loom Wiki Pilot eighth end-to-end smoke test, after restarting the stale mux MCP proxy on Nanite's side. This fragment tests whether Curator can now fetch fragment content and reach Loom's compile API to produce a real compiled wiki page.

## Verifications

| Check | Status | Message |
|---|---|---|
| heading | passed | page contains an H1 matching the title |
| summary | passed | page summary is present |
| source | warning | page source is empty |
| links | info | 0 links extracted |
