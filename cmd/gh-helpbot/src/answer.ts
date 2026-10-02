import { installFetchBase } from "../../../ui/src/cli/fetchBase";
import { askDocsAgent, loadTools, type HeadlessRun } from "../../../ui/src/cli/headless";
import type { ToolDefinition } from "../../../ui/src/lib/chatApi";

import type { GitHubClient } from "./github";
import type { Job } from "./job";
import { log } from "./log";
import { formatReply } from "./reply";

/**
 * From a Job to a posted comment. Every trigger mode ends up in handleJob().
 */

export interface AnswerConfig {
  baseUrl: string;
  apiKey?: string;
  model: string;
  mention: string;
  maxIterations?: number;
  timeoutMs?: number;
  docsUrl?: string;
}

/** How much of an opening post is handed to the model when the mention had no question of its own. */
const THREAD_BODY_LIMIT = 6000;

/** The user message the agent sees. */
export function buildQuestion(job: Job): string {
  let question = job.question.trim();
  if (!question) {
    // The comment was just the mention: the thread itself is the question.
    const body = job.threadBody.trim();
    question = `${job.title}\n\n${body.length > THREAD_BODY_LIMIT ? body.slice(0, THREAD_BODY_LIMIT) + "\n[...]" : body}`.trim();
  }
  return `${question}\n\n(Asked on GitHub ${job.kind} #${job.number} "${job.title}" in ${job.repo}.)`;
}

export class Answerer {
  private tools: ToolDefinition[] = [];

  constructor(private readonly cfg: AnswerConfig) {}

  /** Points the shared browser modules at llama-swap and checks it serves the docs tools. */
  async init(): Promise<void> {
    installFetchBase({ baseUrl: this.cfg.baseUrl, apiKey: this.cfg.apiKey });
    this.tools = await loadTools(this.cfg.baseUrl);
    log("agent ready", { baseUrl: this.cfg.baseUrl, model: this.cfg.model, tools: this.tools.length });
  }

  /** The reply body, or undefined when there is nothing worth posting. */
  async answer(job: Job): Promise<{ body: string; run: HeadlessRun } | undefined> {
    const run = await askDocsAgent(
      buildQuestion(job),
      {
        model: this.cfg.model,
        maxIterations: this.cfg.maxIterations,
        timeoutMs: this.cfg.timeoutMs,
      },
      this.tools,
    );
    const fields = {
      job: job.id,
      iterations: run.iterations,
      tools: run.toolCalls.map((t) => t.name).join(","),
      seconds: (run.durationMs / 1000).toFixed(1),
      done: run.doneReason,
    };
    if (run.error || !run.answer.trim()) {
      // An error, a timeout or an empty turn: say nothing rather than post noise.
      log("agent: no answer", { ...fields, error: run.error ?? "empty answer" });
      return undefined;
    }
    log("agent: answered", fields);
    return {
      body: formatReply(run.answer, { id: job.id, model: this.cfg.model, mention: this.cfg.mention, docsUrl: this.cfg.docsUrl }),
      run,
    };
  }
}

export async function postReply(client: GitHubClient, job: Job, body: string): Promise<{ url: string }> {
  if (job.kind === "issue") {
    return client.postIssueComment(job.repo, job.number, body);
  }
  let replyToId = job.parentCommentNodeId;
  if (!replyToId && job.commentNodeId) {
    replyToId =
      job.parentCommentDbId !== undefined ? await client.discussionThreadRoot(job.commentNodeId) : job.commentNodeId;
  }
  return client.postDiscussionComment(job.subjectNodeId, body, replyToId);
}

export interface HandleOptions {
  answerer: Answerer;
  /** Absent in a dry run. */
  client?: GitHubClient;
  dryRun: boolean;
}

/** Answers and posts one job. Returns true when a reply was posted (or printed, in a dry run). */
export async function handleJob(job: Job, opts: HandleOptions): Promise<boolean> {
  log("answering", { job: job.id, repo: job.repo, [job.kind]: job.number, source: job.source, url: job.htmlUrl });
  const result = await opts.answerer.answer(job);
  if (!result) return false;

  if (opts.dryRun || !opts.client) {
    process.stdout.write(`--- dry run: reply to ${job.htmlUrl}\n${result.body}\n---\n`);
    return true;
  }
  const posted = await postReply(opts.client, job, result.body);
  log("posted", { job: job.id, url: posted.url });
  return true;
}
