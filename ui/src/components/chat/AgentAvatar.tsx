import { useState, useEffect, type CSSProperties } from 'react'
import { useAvatarState, type AvatarState } from '@/hooks/useAvatarState'

// ── Theme B — single sheet ────────────────────────────────────────────────
import spritesBUrl from '@/assets/sprites/theme-b/sprites-b.png'

// ── Theme A — per-state strips ────────────────────────────────────────────
import stripIdleUrl      from '@/assets/sprites/theme-a/strip_idle.png'
import stripThinkingUrl  from '@/assets/sprites/theme-a/strip_thinking.png'
import stripTypingUrl    from '@/assets/sprites/theme-a/strip_typing.png'
import stripListeningUrl from '@/assets/sprites/theme-a/strip_listening.png'
import stripTeachingUrl  from '@/assets/sprites/theme-a/strip_teaching.png'
import stripConfusedUrl  from '@/assets/sprites/theme-a/strip_confused.png'
import stripErrorUrl     from '@/assets/sprites/theme-a/strip_error.png'

export const AVATAR_SPRITE_SET_EVENT = 'nanite:avatarSpriteSetChanged'
export type SpriteSet = 'set-b' | 'set-alt'

// ─────────────────────────────────────────────────────────────────────────────
// Set B layout — sprites-b.png is 1536×1024, 12 cols × 5 rows
// (each state is 6 frames sitting in *half* a row).
//   row 0:  idle      cols 0-5  | thinking    cols 6-11
//   row 1:  typing    cols 0-5  | teaching    cols 6-11
//   row 2:  quizzical cols 0-5  | confused    cols 6-11
//   row 3:  error     cols 0-5  | listening   cols 6-11
//   row 4:  sleep     cols 0-5  | celebrating cols 6-11
//
// Source cell is 128×204.8; the robot itself is ~99×111 centered around
// source (77, 130). bg-size 600×400 is a uniform 0.39× scale of the sheet
// (matches the sheet's natural 3:2 aspect, so the sprite's own frame stays
// square in display). Robot ends up ~39×43 inside the 36px container —
// fills the box, with antennae/feet overflowing a couple px (clipped by
// the rounded container).
// ─────────────────────────────────────────────────────────────────────────────
const SET_B_LAYOUT: Record<AvatarState, { row: number; colOffset: number }> = {
  idle:        { row: 0, colOffset: 0 },
  thinking:    { row: 0, colOffset: 6 },
  tool_active: { row: 1, colOffset: 0 }, // typing
  confused:    { row: 2, colOffset: 6 },
  error:       { row: 3, colOffset: 0 },
  listening:   { row: 3, colOffset: 6 },
  celebrating: { row: 4, colOffset: 6 },
}
const SET_B_FRAMES         = 6
const SET_B_FRAME_WIDTH    = 50.0  // 600 / 12
const SET_B_ROW_HEIGHT     = 80.0  // 400 / 5
// Robot center within cell at scale 0.39: x≈30, y≈51.
// Container center is at (18,18). +1 left nudge baked into X.
const SET_B_ROBOT_X_OFFSET = 13    // (30 - 18) + 1px-left nudge
const SET_B_ROBOT_Y_OFFSET = 33    // 51 - 18

// ─────────────────────────────────────────────────────────────────────────────
// Set Alt — per-state strips (theme-a)
// Strips are 1496×256, six robots spaced exactly 248 source-px center-to-center
// (uniform — these were generated programmatically, so no per-frame jitter).
// Robot is 192 wide × 216 tall centered at source (127.5, 121.5).
//   bg-size 281×48 → scale 0.1875
//   frame in display: 46.5 wide
//   robot in display: 36 wide × 40.5 tall (fills width; antennae/feet
//                                          may clip a couple px)
// Frame width (46.5) > container width (36) so no adjacent-frame bleed.
// ─────────────────────────────────────────────────────────────────────────────
const SET_ALT_STRIPS: Record<AvatarState, string> = {
  idle:        stripIdleUrl,
  thinking:    stripThinkingUrl,
  tool_active: stripTypingUrl,
  listening:   stripListeningUrl,
  celebrating: stripTeachingUrl,
  error:       stripErrorUrl,
  confused:    stripConfusedUrl,
}
const SET_ALT_FRAMES        = 6
const SET_ALT_FRAME_WIDTH   = 46.5  // 248 source * 0.1875
const SET_ALT_ROBOT_X_OFFSET = 6    // 127.5 * 0.1875 - 18 ≈ 6
const SET_ALT_ROBOT_Y_OFFSET = -2   // shifted down for margin above head

// ─────────────────────────────────────────────────────────────────────────────
// Per-state walk-cycle speed (ms per full 6-frame cycle).
// ─────────────────────────────────────────────────────────────────────────────
const SPEEDS: Record<AvatarState, number> = {
  idle:        1400,
  thinking:     700,
  tool_active:  450,
  listening:   1000,
  celebrating:  350,
  error:        280,
  confused:     950,
}

function readSpriteSet(): SpriteSet {
  if (typeof window === 'undefined') return 'set-alt'
  return (localStorage.getItem('nanite:avatarSpriteSet') as SpriteSet | null) ?? 'set-alt'
}

export function AgentAvatar({ className }: { className?: string }) {
  const state = useAvatarState()
  const [spriteSet, setSpriteSet] = useState<SpriteSet>(readSpriteSet)
  const [frame, setFrame] = useState(0)

  useEffect(() => {
    function onChanged() { setSpriteSet(readSpriteSet()) }
    window.addEventListener(AVATAR_SPRITE_SET_EVENT, onChanged)
    return () => window.removeEventListener(AVATAR_SPRITE_SET_EVENT, onChanged)
  }, [])

  const speed = SPEEDS[state]

  useEffect(() => {
    setFrame(0)
    const frames = spriteSet === 'set-b' ? SET_B_FRAMES : SET_ALT_FRAMES
    const id = window.setInterval(() => {
      setFrame(f => (f + 1) % frames)
    }, speed / frames)
    return () => clearInterval(id)
  }, [spriteSet, state, speed])

  let spriteStyle: CSSProperties

  if (spriteSet === 'set-b') {
    const { row, colOffset } = SET_B_LAYOUT[state]
    const bgX = -((colOffset + frame) * SET_B_FRAME_WIDTH + SET_B_ROBOT_X_OFFSET)
    const bgY = -(row * SET_B_ROW_HEIGHT + SET_B_ROBOT_Y_OFFSET)
    spriteStyle = {
      backgroundImage:     `url(${spritesBUrl})`,
      backgroundSize:      '600px 400px',
      backgroundPositionX: bgX,
      backgroundPositionY: bgY,
      backgroundRepeat:    'no-repeat',
      imageRendering:      'auto',
    }
  } else {
    const bgX = -(frame * SET_ALT_FRAME_WIDTH + SET_ALT_ROBOT_X_OFFSET)
    const bgY = -SET_ALT_ROBOT_Y_OFFSET
    spriteStyle = {
      backgroundImage:     `url(${SET_ALT_STRIPS[state]})`,
      backgroundSize:      '281px 48px',
      backgroundPositionX: bgX,
      backgroundPositionY: bgY,
      backgroundRepeat:    'no-repeat',
      imageRendering:      'pixelated',
    }
  }

  return (
    <div
      className={`w-9 h-9 shrink-0 rounded-[8px] overflow-hidden bg-no-repeat${className ? ` ${className}` : ''}`}
      style={spriteStyle}
      title={state}
    />
  )
}
