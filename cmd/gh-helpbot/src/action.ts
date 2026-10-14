import { readFile } from "node:fs/promises";

import { handleJob, type HandleOptions } from "./answer";
import { log } from "./log";
import { parseEvent, type ParseOptions } from "./webhook";

/**
 * `action`: one delivery, handed over by the GitHub Actions runner through
 * GITHUB_EVENT_NAME and the JSON file at GITHUB_EVENT_PATH.
 */

export interface ActionEnv {
  GITHUB_EVENT_NAME?: string;
  GITHUB_EVENT_PATH?: string;
}

export async function loadActionEvent(env: ActionEnv): Promise<{ event: string; payload: unknown }> {
  const event = env.GITHUB_EVENT_NAME;
  const file = env.GITHUB_EVENT_PATH;
  if (!event || !file) {
    throw new Error("GITHUB_EVENT_NAME and GITHUB_EVENT_PATH are not set; `action` runs inside a GitHub Actions job");
  }
  return { event, payload: JSON.parse(await readFile(file, "utf8")) };
}

/** Exit status: 0 when nothing needed doing or a reply was posted, 1 when answering or posting failed. */
export async function runOnce(
  event: string,
  payload: unknown,
  parse: ParseOptions,
  handle: HandleOptions,
): Promise<number> {
  const parsed = parseEvent(event, payload, parse);
  if ("skip" in parsed) {
    log("skipped", { event, reason: parsed.skip });
    return 0;
  }
  const done = await handleJob(parsed.job, handle);
  return done ? 0 : 1;
}
