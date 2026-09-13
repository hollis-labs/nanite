export interface TranscriptPosition {
  anchorId: string | null;
  offset: number;
  atBottom: boolean;
  oldestOffset: number;
}

const STORAGE_KEY = "nanite:transcript-positions";

export function readTranscriptPosition(sessionId: string): TranscriptPosition | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(STORAGE_KEY) ?? "{}")[sessionId];
    if (
      value &&
      typeof value.atBottom === "boolean" &&
      Number.isFinite(value.offset) &&
      Number.isInteger(value.oldestOffset) &&
      value.oldestOffset >= 0 &&
      (value.anchorId === null || typeof value.anchorId === "string")
    )
      return value;
  } catch {
    /* Storage is optional; navigation still works without it. */
  }
  return null;
}

export function writeTranscriptPosition(sessionId: string, position: TranscriptPosition) {
  try {
    const positions = JSON.parse(sessionStorage.getItem(STORAGE_KEY) ?? "{}");
    delete positions[sessionId];
    positions[sessionId] = position;
    // Retain only small viewport records, never transcript contents.
    const entries = Object.entries(positions).slice(-20);
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(entries)));
  } catch {
    /* Ignore unavailable/full storage. */
  }
}
