import type { GitHubClient, SnapshotComment, ThreadSnapshot } from "./github";
import type { Job } from "./job";
import { log } from "./log";
import { findMention } from "./mention";
import { hasMarker } from "./reply";
import { loadState, markHandled, saveState, type PollState } from "./state";
import { handleJob, type HandleOptions } from "./answer";

/**
 * `poll`: no inbound port, just the GitHub API on a timer.
 */

export interface CollectOptions {
  since: string;
  mention: string;
  selfLogin?: string;
  handled: ReadonlySet<string>;
}

interface Candidate {
  id: string;
  body: string;
  updatedAt: string;
  url: string;
  author: SnapshotComment["author"];
  commentNodeId?: string;
  parentCommentNodeId?: string;
}

function candidates(thread: ThreadSnapshot): Candidate[] {
  const out: Candidate[] = [
    { id: thread.id, body: thread.body, updatedAt: thread.updatedAt, url: thread.url, author: thread.author },
  ];
  for (const comment of thread.comments) {
    out.push({ ...comment, commentNodeId: comment.id });
    for (const reply of comment.replies ?? []) {
      out.push({ ...reply, commentNodeId: reply.id, parentCommentNodeId: comment.id });
    }
  }
  return out;
}

function allBodies(thread: ThreadSnapshot): string[] {
  const out: string[] = [];
  for (const comment of thread.comments) {
    out.push(comment.body);
    for (const reply of comment.replies ?? []) out.push(reply.body);
  }
  return out;
}

/**
 * Picks the mentions in a snapshot that still need an answer. Pure.
 *
 * A candidate is skipped when it is older than `since`, written by the bot or
 * another bot, already in `handled`, or already answered in its thread (a
 * reply carrying its marker exists). The marker check is what makes a lost
 * state file harmless.
 */
export function collectJobs(threads: ThreadSnapshot[], opts: CollectOptions): Job[] {
  const sinceMs = Date.parse(opts.since);
  const jobs: Job[] = [];
  for (const thread of threads) {
    const bodies = allBodies(thread);
    for (const c of candidates(thread)) {
      if (Date.parse(c.updatedAt) < sinceMs) continue;
      if (c.author?.isBot) continue;
      if (opts.selfLogin && c.author?.login === opts.selfLogin) continue;
      if (opts.handled.has(c.id)) continue;
      if (bodies.some((body) => hasMarker(body, c.id))) continue;
      const m = findMention(c.body, opts.mention);
      if (!m.found) continue;
      const job: Job = {
        id: c.id,
        kind: thread.kind,
        repo: thread.repo,
        number: thread.number,
        title: thread.title,
        threadBody: thread.body,
        question: m.query,
        subjectNodeId: thread.id,
        htmlUrl: c.url,
        source: "poll",
      };
      if (c.commentNodeId) job.commentNodeId = c.commentNodeId;
      if (c.parentCommentNodeId) job.parentCommentNodeId = c.parentCommentNodeId;
      jobs.push(job);
    }
  }
  return jobs;
}

export interface PollOptions {
  client: GitHubClient;
  repos: string[];
  stateFile: string;
  /** Used when the state file does not exist yet. Default: now. */
  since?: string;
  intervalMs: number;
  once: boolean;
  mention: string;
  selfLogin?: string;
  handle: HandleOptions;
  /** Injectable for tests. */
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
}

function defaultSleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve();
    const timer = setTimeout(done, ms);
    function done() {
      signal.removeEventListener("abort", done);
      clearTimeout(timer);
      resolve();
    }
    signal.addEventListener("abort", done, { once: true });
  });
}

/** One pass over every repository. Returns the state to persist. */
export async function pollOnce(state: PollState, opts: PollOptions, stopping: AbortSignal): Promise<PollState> {
  const tickStart = new Date().toISOString();
  const handled = new Set(state.handled);
  let next = state;
  let posted = 0;

  for (const repo of opts.repos) {
    const threads = await opts.client.recentThreads(repo, state.since);
    const jobs = collectJobs(threads, { since: state.since, mention: opts.mention, selfLogin: opts.selfLogin, handled });
    if (jobs.length) log("poll: found mentions", { repo, count: jobs.length, threads: threads.length });
    for (const job of jobs) {
      if (stopping.aborted) return next;
      let done = false;
      try {
        done = await handleJob(job, opts.handle);
      } catch (error) {
        log("poll: job failed", { job: job.id, error: error instanceof Error ? error.message : String(error) });
      }
      if (done) {
        posted++;
        next = markHandled(next, job.id);
        handled.add(job.id);
        await saveState(opts.stateFile, next);
      }
    }
  }

  // The window only moves once the whole tick succeeded, so an API error
  // makes the next tick look at the same window again.
  next = { since: tickStart, handled: next.handled };
  await saveState(opts.stateFile, next);
  if (posted) log("poll: tick done", { posted, since: next.since });
  return next;
}

export async function runPoll(opts: PollOptions, stopping: AbortSignal): Promise<void> {
  const sleep = opts.sleep ?? defaultSleep;
  let state = await loadState(opts.stateFile);
  if (!state) {
    state = { since: opts.since ?? new Date().toISOString(), handled: [] };
    log("poll: no state file, starting fresh", { file: opts.stateFile, since: state.since });
  } else {
    log("poll: resuming", { file: opts.stateFile, since: state.since, handled: state.handled.length });
  }

  while (!stopping.aborted) {
    try {
      state = await pollOnce(state, opts, stopping);
    } catch (error) {
      log("poll: tick failed", { error: error instanceof Error ? error.message : String(error) });
    }
    if (opts.once) return;
    await sleep(opts.intervalMs, stopping);
  }
}
