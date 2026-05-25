/**
 * useShellStore — shell-exec output surface for the Terminal-1 tab in
 * `ChatWorkingDrawer`.  Composer-side code (`ChatComposer.tsx`) calls
 * `appendShellOutput` per streamed chunk and toggles `shellRunning`
 * around an exec; the drawer's terminal tab subscribes and renders.
 *
 * Output is held as an array of chunks (not a single concatenated
 * string) so per-chunk appends are O(1) instead of O(n²) on the total
 * buffer length.  A max-chunks cap drops the oldest entries when
 * exceeded so the buffer stays bounded for long-running commands.
 */

import { create } from 'zustand'

/** Maximum number of streamed chunks retained before the oldest are
 *  dropped.  Tuned high enough to cover typical command output without
 *  growing without bound on a runaway tail/stream. */
export const SHELL_MAX_CHUNKS = 2000

export interface ShellState {
  /** Per-session Terminal-1 state. */
  sessions: Record<string, {
    shellChunks: string[]
    shellRunning: boolean
    pendingShellCommand: string | null
  }>
  /** Append-only chunk buffer (stdout + stderr interleaved by arrival
   *  order, with line breaks preserved in the chunks themselves).
   *  Render with `chunks.join('')`. */
  shellChunks: string[]
  /** True while a `!command` exec is in flight. */
  shellRunning: boolean
  /** The command that's currently running (or last queued). */
  pendingShellCommand: string | null
  /** Append a chunk of streamed output. Bounded by `SHELL_MAX_CHUNKS`. */
  appendShellOutput: (sessionID: string, chunk: string) => void
  /** Reset the buffer (e.g., when the user clears the terminal tab). */
  clearShellOutput: (sessionID: string) => void
  /** Toggle the running flag. */
  setShellRunning: (sessionID: string, running: boolean) => void
  /** Set / clear the pending-command label. */
  setPendingShellCommand: (sessionID: string, command: string | null) => void
}

export const useShellStore = create<ShellState>((set) => ({
  sessions: {},
  shellChunks: [],
  shellRunning: false,
  pendingShellCommand: null,
  appendShellOutput: (sessionID, chunk) =>
    set((s) => {
      const current = s.sessions[sessionID] ?? {
        shellChunks: [],
        shellRunning: false,
        pendingShellCommand: null,
      }
      const next = current.shellChunks.concat(chunk)
      // Drop the oldest entries if we've blown past the cap.  slice() is
      // O(k) where k = SHELL_MAX_CHUNKS, so amortized append stays O(1).
      const shellChunks = next.length > SHELL_MAX_CHUNKS
        ? next.slice(next.length - SHELL_MAX_CHUNKS)
        : next
      return {
        sessions: {
          ...s.sessions,
          [sessionID]: { ...current, shellChunks },
        },
        shellChunks,
      }
    }),
  clearShellOutput: (sessionID) =>
    set((s) => {
      const current = s.sessions[sessionID] ?? {
        shellChunks: [],
        shellRunning: false,
        pendingShellCommand: null,
      }
      return {
        sessions: {
          ...s.sessions,
          [sessionID]: { ...current, shellChunks: [] },
        },
        shellChunks: [],
      }
    }),
  setShellRunning: (sessionID, shellRunning) =>
    set((s) => {
      const current = s.sessions[sessionID] ?? {
        shellChunks: [],
        shellRunning: false,
        pendingShellCommand: null,
      }
      return {
        sessions: {
          ...s.sessions,
          [sessionID]: { ...current, shellRunning },
        },
        shellRunning,
      }
    }),
  setPendingShellCommand: (sessionID, pendingShellCommand) =>
    set((s) => {
      const current = s.sessions[sessionID] ?? {
        shellChunks: [],
        shellRunning: false,
        pendingShellCommand: null,
      }
      return {
        sessions: {
          ...s.sessions,
          [sessionID]: { ...current, pendingShellCommand },
        },
        pendingShellCommand,
      }
    }),
}))
