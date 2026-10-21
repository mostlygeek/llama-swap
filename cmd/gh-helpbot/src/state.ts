import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";

/**
 * The poller's memory between ticks and restarts.
 *
 * `since` is the start of the last tick that completed; `handled` is a
 * bounded list of ids already answered, a safety net for the case where a
 * thread is updated again before `since` moves past the triggering comment.
 * The answered: marker in posted replies covers the case where this file is
 * lost altogether.
 */
export interface PollState {
  since: string;
  handled: string[];
}

export const HANDLED_CAP = 2000;

export async function loadState(file: string): Promise<PollState | undefined> {
  let raw: string;
  try {
    raw = await readFile(file, "utf8");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
    throw error;
  }
  const parsed = JSON.parse(raw) as Partial<PollState>;
  if (typeof parsed.since !== "string" || Number.isNaN(Date.parse(parsed.since))) {
    throw new Error(`${file}: "since" is not a timestamp`);
  }
  return {
    since: parsed.since,
    handled: Array.isArray(parsed.handled) ? parsed.handled.filter((h) => typeof h === "string") : [],
  };
}

/** Writes to a sibling temp file and renames, so a crash mid-write cannot leave a truncated state. */
export async function saveState(file: string, state: PollState): Promise<void> {
  const trimmed: PollState = {
    since: state.since,
    handled: state.handled.slice(-HANDLED_CAP),
  };
  await mkdir(path.dirname(file), { recursive: true });
  const tmp = `${file}.${process.pid}.tmp`;
  await writeFile(tmp, JSON.stringify(trimmed, null, 2) + "\n");
  await rename(tmp, file);
}

export function markHandled(state: PollState, id: string): PollState {
  if (state.handled.includes(id)) return state;
  return { since: state.since, handled: [...state.handled, id].slice(-HANDLED_CAP) };
}
