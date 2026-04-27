/**
 * Regression tests for CW-20260421-0008 — TipTap slash command injection
 * reliability fix.
 *
 * Root causes addressed:
 * 1. menuRef null race: `root.render()` in React 18 doesn't guarantee
 *    synchronous ref callbacks, so the first onKeyDown after opening the menu
 *    found `menuRef = null` and fell through, losing the Enter/arrow selection.
 *    Fix: wrap `r.render()` in `flushSync` so the ref is populated before
 *    control returns to TipTap.
 *
 * 2. No "without autocomplete" path: if the user dismissed the menu (Escape or
 *    click-away) but left the /command-name text in the editor and pressed
 *    Enter, handleSend just passed the raw "/foo" string to onSend. The agent
 *    received a literal slash command string, not the injected skill payload.
 *    Fix: handleSend now detects a leading "/" and routes through handleCommand.
 *
 * 3. Skill injection silent failure: when api.getSkill() returned a skill with
 *    no `prompt` field (DB-only skill or skill with empty body), nothing was
 *    sent to the agent and no error was surfaced.
 *    Fix: added else-branch to fall back to result.content when prompt is falsy.
 *
 * These tests exercise the pure logic units (parseSlashText, skill fallback)
 * without a DOM/TipTap instance. The flushSync fix is verified via the manual
 * test sequence below.
 *
 * Manual test sequence (CW-20260421-0008 acceptance):
 *  1. Open a chat session with the interview-protocol skill registered.
 *  2. Type "/" — the command menu opens.
 *  3. Type "int" to filter to "interview-protocol".
 *  4. Press Enter to select — the skill prompt should inject into the agent
 *     message immediately. Verify the agent receives the full skill prompt text,
 *     not a raw slug.
 *  5. Repeat steps 2–4 four more times in the same session without reloading.
 *     Each invocation must inject the prompt (not a blank turn).
 *  6. Type "/interview-protocol", press Escape to dismiss the menu, then press
 *     Enter. The skill prompt should still inject (without-autocomplete path).
 *  7. With the menu open, press Enter immediately after "/" (before items load).
 *     Confirm no crash and the command either executes or gracefully no-ops.
 */

import { describe, expect, it } from 'vitest'

// ---------------------------------------------------------------------------
// Pure-logic unit: parseSlashText
// Mirrors the logic added to handleSend for the without-autocomplete path.
// ---------------------------------------------------------------------------

function parseSlashText(text: string): { cmdName: string; args: string } | null {
  if (!text.startsWith('/') || text.length <= 1) return null
  const withoutSlash = text.slice(1)
  const spaceIdx = withoutSlash.indexOf(' ')
  const cmdName = spaceIdx === -1 ? withoutSlash : withoutSlash.slice(0, spaceIdx)
  if (!cmdName) return null
  const args = spaceIdx === -1 ? '' : withoutSlash.slice(spaceIdx + 1).trim()
  return { cmdName, args }
}

describe('parseSlashText — without-autocomplete command detection', () => {
  it('returns null for plain text', () => {
    expect(parseSlashText('hello world')).toBeNull()
  })

  it('returns null for bare "/"', () => {
    expect(parseSlashText('/')).toBeNull()
  })

  it('returns null for empty string', () => {
    expect(parseSlashText('')).toBeNull()
  })

  it('parses /command-name with no args', () => {
    const result = parseSlashText('/interview-protocol')
    expect(result).not.toBeNull()
    expect(result?.cmdName).toBe('interview-protocol')
    expect(result?.args).toBe('')
  })

  it('parses /command-name with args', () => {
    const result = parseSlashText('/compact --hard')
    expect(result).not.toBeNull()
    expect(result?.cmdName).toBe('compact')
    expect(result?.args).toBe('--hard')
  })

  it('parses /new (single-word command)', () => {
    const result = parseSlashText('/new')
    expect(result).not.toBeNull()
    expect(result?.cmdName).toBe('new')
  })

  it('does not treat shell "!cmd" as slash command', () => {
    expect(parseSlashText('!ls -la')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// Pure-logic unit: skill injection fallback
// Mirrors the three result paths in handleCommand's default case.
// ---------------------------------------------------------------------------

interface FakeSkill {
  prompt?: string
}

interface FakeCommandResult {
  action: string
  content?: string
}

function resolveSkillMessage(result: FakeCommandResult, skill: FakeSkill | null): string | null {
  if (result.action !== 'skill') return null
  const parts = (result.content ?? '').trim().split(/\s+/)
  const args = parts.slice(1).join(' ')
  if (skill?.prompt) {
    return args ? `${skill.prompt}\n\nArgs: ${args}` : skill.prompt
  }
  // Fallback: skill not found or no prompt — send raw content slug.
  // Guard empty content (matches `if (result.content)` in production code).
  return result.content || null
}

describe('resolveSkillMessage — skill injection with fallback (CW-20260421-0008)', () => {
  it('injects skill.prompt when skill is found and has a prompt', () => {
    const result: FakeCommandResult = { action: 'skill', content: 'interview-protocol' }
    const skill: FakeSkill = { prompt: 'You are running the interview protocol...' }
    const msg = resolveSkillMessage(result, skill)
    expect(msg).toBe('You are running the interview protocol...')
  })

  it('appends args to prompt when content has trailing args', () => {
    const result: FakeCommandResult = { action: 'skill', content: 'interview-protocol topic=foo' }
    const skill: FakeSkill = { prompt: 'Protocol prompt here.' }
    const msg = resolveSkillMessage(result, skill)
    expect(msg).toBe('Protocol prompt here.\n\nArgs: topic=foo')
  })

  it('falls back to result.content when skill has no prompt (DB-only skill)', () => {
    const result: FakeCommandResult = { action: 'skill', content: 'interview-protocol' }
    const skill: FakeSkill = { prompt: '' }  // empty prompt
    const msg = resolveSkillMessage(result, skill)
    expect(msg).toBe('interview-protocol')
  })

  it('falls back to result.content when skill fetch returns null', () => {
    const result: FakeCommandResult = { action: 'skill', content: 'interview-protocol' }
    const msg = resolveSkillMessage(result, null)
    expect(msg).toBe('interview-protocol')
  })

  it('returns null for non-skill actions', () => {
    const result: FakeCommandResult = { action: 'message', content: 'some message' }
    const msg = resolveSkillMessage(result, null)
    expect(msg).toBeNull()
  })

  it('returns null when content is empty and skill has no prompt', () => {
    const result: FakeCommandResult = { action: 'skill', content: '' }
    const msg = resolveSkillMessage(result, null)
    expect(msg).toBeNull()
  })
})
