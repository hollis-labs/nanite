---
name: playwright-bowser
description: Headless browser automation using Playwright CLI. Use when you need headless browsing, parallel browser sessions, UI testing, screenshots, web scraping, or browser automation that can run in the background. Keywords - playwright, headless, browser, test, screenshot, scrape, parallel.
allowed-tools: Bash
---

# Playwright Bowser

## Purpose

Automate browsers using `playwright-cli` — a token-efficient CLI for Playwright. Runs headless by default, supports parallel sessions via named sessions (`-s=`), and doesn't load tool schemas into context.

## Key Details

- **Headless by default** — pass `--headed` to `open` to see the browser
- **Parallel sessions** — use `-s=<name>` to run multiple independent browser instances
- **Persistent profiles** — cookies and storage state preserved between calls
- **Token-efficient** — CLI-based, no accessibility trees or tool schemas in context
- **Vision mode** (opt-in) — set `PLAYWRIGHT_MCP_CAPS=vision` to receive screenshots as image responses in context instead of just saving to disk

## Sessions

**Always use a named session.** Derive a short, descriptive kebab-case name from the user's prompt. This gives each task a persistent browser profile (cookies, localStorage, history) that accumulates across calls.

```bash
# Derive session name from prompt context:
# "test the volon dashboard" → -s=volon-dashboard
# "UI test the tasks page"  → -s=volon-tasks

playwright-cli -s=volon-dashboard open http://localhost:5173 --persistent
playwright-cli -s=volon-dashboard snapshot
playwright-cli -s=volon-dashboard click e12
```

Managing sessions:
```bash
playwright-cli list                      # list all sessions
playwright-cli close-all                 # close all sessions
playwright-cli -s=<name> close           # close specific session
playwright-cli -s=<name> delete-data     # wipe session profile
```

## Quick Reference

```
Core:       open [url], goto <url>, click <ref>, fill <ref> <text>, type <text>, snapshot, screenshot [ref], close
Navigate:   go-back, go-forward, reload
Keyboard:   press <key>, keydown <key>, keyup <key>
Mouse:      mousemove <x> <y>, mousedown, mouseup, mousewheel <dx> <dy>
Tabs:       tab-list, tab-new [url], tab-close [index], tab-select <index>
Save:       screenshot [ref], pdf, screenshot --filename=f
Storage:    state-save, state-load, cookie-*, localstorage-*, sessionstorage-*
Network:    route <pattern>, route-list, unroute, network
DevTools:   console, run-code <code>, tracing-start/stop, video-start/stop
Sessions:   -s=<name> <cmd>, list, close-all, kill-all
Config:     open --headed, open --browser=chrome, resize <w> <h>
```

## Workflow

1. Derive a session name from the user's prompt and open with `--persistent` to preserve cookies/state. Always set the viewport via env var at launch:
```bash
PLAYWRIGHT_MCP_VIEWPORT_SIZE=1440x900 playwright-cli -s=<session-name> open <url> --persistent
# or headed:
PLAYWRIGHT_MCP_VIEWPORT_SIZE=1440x900 playwright-cli -s=<session-name> open <url> --persistent --headed
# or with vision (screenshots returned as image responses in context):
PLAYWRIGHT_MCP_VIEWPORT_SIZE=1440x900 PLAYWRIGHT_MCP_CAPS=vision playwright-cli -s=<session-name> open <url> --persistent
```

2. Get element references via snapshot:
```bash
playwright-cli snapshot
```

3. Interact using refs from snapshot:
```bash
playwright-cli click <ref>
playwright-cli fill <ref> "text"
playwright-cli type "text"
playwright-cli press Enter
```

4. Capture results:
```bash
playwright-cli screenshot
playwright-cli screenshot --filename=output.png
```

5. **Always close the session when done.**
```bash
playwright-cli -s=<session-name> close
```

## Fragments Engine GUI Defaults

- Volon GUI dev server: `http://localhost:1420`
- Volon API server: `http://localhost:8085`
- Mentat GUI dev server: check Cerberus for current port
- Screenshots output: `./screenshots/bowser-qa/`

## Full Help

Run `playwright-cli --help` or `playwright-cli --help <command>` for detailed command usage.
