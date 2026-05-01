/**
 * useShellStore — shell-exec output surface (Task 6 / Step 3 of the
 * 2026-05-01 chat-surface redesign).
 *
 * Lives outside `useChatStore` so the streamed output a `!command`
 * banner produces (currently held inside `ChatComposer.tsx` local
 * state) can be subscribed to by the new `ChatWorkingDrawer`'s
 * Terminal-1 tab without reaching into the composer.
 *
 * Wave 4 (Task 11) wires the composer side — it calls
 * `appendShellOutput` per streamed chunk and toggles `shellRunning`
 * around an exec. Until then this store sits idle and the
 * Terminal-1 tab renders the empty-state copy.
 */

import { create } from 'zustand'

export interface ShellState {
  /** Accumulated streamed shell output (stdout + stderr interleaved
   *  by arrival order, with line breaks preserved). */
  shellOutput: string
  /** True while a `!command` exec is in flight. */
  shellRunning: boolean
  /** The command that's currently running (or last queued), surfaced
   *  for the running-banner / Terminal-1 header. Null when idle. */
  pendingShellCommand: string | null
  /** Append a chunk of streamed output. Caller is responsible for
   *  including its own trailing newline if line-oriented. */
  appendShellOutput: (line: string) => void
  /** Reset the buffer (e.g. when the user clears the terminal tab). */
  clearShellOutput: () => void
  /** Toggle the running flag. */
  setShellRunning: (running: boolean) => void
  /** Set / clear the pending-command label. */
  setPendingShellCommand: (command: string | null) => void
}

export const useShellStore = create<ShellState>((set) => ({
  shellOutput: '',
  shellRunning: false,
  pendingShellCommand: null,
  appendShellOutput: (line) =>
    set((s) => ({ shellOutput: s.shellOutput + line })),
  clearShellOutput: () => set({ shellOutput: '' }),
  setShellRunning: (shellRunning) => set({ shellRunning }),
  setPendingShellCommand: (pendingShellCommand) => set({ pendingShellCommand }),
}))
