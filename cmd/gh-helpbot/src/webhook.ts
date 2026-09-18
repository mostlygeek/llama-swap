import { createHmac, timingSafeEqual } from "node:crypto";
import http from "node:http";

import { findMention } from "./mention";
import { log } from "./log";
import type { Job } from "./job";

/**
 * The webhook side of `serve`: signature checking, turning a delivery into a
 * Job, and the small HTTP server that receives them.
 */

export const MAX_BODY_BYTES = 1024 * 1024;

/** `X-Hub-Signature-256` is `sha256=<hex hmac of the raw body>`. */
export function verifySignature(secret: string, body: Buffer, header: string | undefined): boolean {
  if (!header || !header.startsWith("sha256=")) return false;
  const expected = createHmac("sha256", secret).update(body).digest("hex");
  const given = header.slice("sha256=".length);
  if (given.length !== expected.length) return false;
  return timingSafeEqual(Buffer.from(given, "utf8"), Buffer.from(expected, "utf8"));
}

export interface ParseOptions {
  mention: string;
  /** The bot's own login; its comments never trigger it. */
  selfLogin?: string;
  /** owner/name allowlist. Empty means every repository the hook delivers for. */
  repos?: string[];
  /** Delivery id or another label for the job's source. */
  source?: string;
}

export type ParseResult = { job: Job } | { skip: string };

interface PayloadUser {
  login?: string;
  type?: string;
}

function authorSkip(user: PayloadUser | undefined, selfLogin: string | undefined): string | undefined {
  if (!user) return undefined;
  if (user.type === "Bot") return `author ${user.login ?? "?"} is a bot`;
  if (selfLogin && user.login === selfLogin) return "author is the bot itself";
  return undefined;
}

/**
 * Reduces one webhook delivery to a Job, or says why not. Pure, so it is
 * shared by `serve`, `action` and `replay` and tested without a server.
 */
export function parseEvent(event: string, payload: any, opts: ParseOptions): ParseResult {
  const repo: string | undefined = payload?.repository?.full_name;
  if (!repo) return { skip: "payload has no repository" };
  if (opts.repos?.length && !opts.repos.includes(repo)) return { skip: `repository ${repo} is not allowed` };

  const source = opts.source ?? "webhook";
  const action: string | undefined = payload?.action;

  switch (event) {
    case "issue_comment": {
      if (action !== "created") return { skip: `issue_comment ${action}` };
      const issue = payload.issue;
      const comment = payload.comment;
      if (!issue || !comment) return { skip: "issue_comment without issue or comment" };
      if (issue.pull_request) return { skip: "comment on a pull request" };
      const skip = authorSkip(comment.user, opts.selfLogin);
      if (skip) return { skip };
      const m = findMention(comment.body, opts.mention);
      if (!m.found) return { skip: "no mention" };
      return {
        job: {
          id: comment.node_id,
          kind: "issue",
          repo,
          number: issue.number,
          title: issue.title ?? "",
          threadBody: issue.body ?? "",
          question: m.query,
          subjectNodeId: issue.node_id,
          commentNodeId: comment.node_id,
          htmlUrl: comment.html_url ?? issue.html_url ?? "",
          source,
        },
      };
    }
    case "issues": {
      if (action !== "opened") return { skip: `issues ${action}` };
      const issue = payload.issue;
      if (!issue) return { skip: "issues without issue" };
      if (issue.pull_request) return { skip: "pull request" };
      const skip = authorSkip(issue.user, opts.selfLogin);
      if (skip) return { skip };
      const m = findMention(issue.body, opts.mention);
      if (!m.found) return { skip: "no mention" };
      return {
        job: {
          id: issue.node_id,
          kind: "issue",
          repo,
          number: issue.number,
          title: issue.title ?? "",
          threadBody: issue.body ?? "",
          question: m.query,
          subjectNodeId: issue.node_id,
          htmlUrl: issue.html_url ?? "",
          source,
        },
      };
    }
    case "discussion_comment": {
      if (action !== "created") return { skip: `discussion_comment ${action}` };
      const discussion = payload.discussion;
      const comment = payload.comment;
      if (!discussion || !comment) return { skip: "discussion_comment without discussion or comment" };
      const skip = authorSkip(comment.user, opts.selfLogin);
      if (skip) return { skip };
      const m = findMention(comment.body, opts.mention);
      if (!m.found) return { skip: "no mention" };
      const job: Job = {
        id: comment.node_id,
        kind: "discussion",
        repo,
        number: discussion.number,
        title: discussion.title ?? "",
        threadBody: discussion.body ?? "",
        question: m.query,
        subjectNodeId: discussion.node_id,
        commentNodeId: comment.node_id,
        htmlUrl: comment.html_url ?? discussion.html_url ?? "",
        source,
      };
      // parent_id is the parent's numeric database id, not a node id; the
      // node id is looked up only when it is needed to post the reply.
      if (typeof comment.parent_id === "number") job.parentCommentDbId = comment.parent_id;
      return { job };
    }
    case "discussion": {
      if (action !== "created") return { skip: `discussion ${action}` };
      const discussion = payload.discussion;
      if (!discussion) return { skip: "discussion without discussion" };
      const skip = authorSkip(discussion.user, opts.selfLogin);
      if (skip) return { skip };
      const m = findMention(discussion.body, opts.mention);
      if (!m.found) return { skip: "no mention" };
      return {
        job: {
          id: discussion.node_id,
          kind: "discussion",
          repo,
          number: discussion.number,
          title: discussion.title ?? "",
          threadBody: discussion.body ?? "",
          question: m.query,
          subjectNodeId: discussion.node_id,
          htmlUrl: discussion.html_url ?? "",
          source,
        },
      };
    }
    default:
      return { skip: `unhandled event ${event}` };
  }
}

export interface WebhookServerOptions {
  secret: string;
  /** URL path the hook is registered on. */
  path: string;
  onDelivery: (event: string, deliveryId: string, payload: unknown) => void;
}

function readBody(req: http.IncomingMessage, limit: number): Promise<Buffer | null> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    req.on("data", (chunk: Buffer) => {
      size += chunk.length;
      if (size > limit) {
        resolve(null);
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

function reply(res: http.ServerResponse, status: number, text: string): void {
  res.writeHead(status, { "Content-Type": "text/plain" });
  res.end(text + "\n");
}

/** Builds the server; the caller listens. GET /healthz answers 200 for a container HEALTHCHECK. */
export function createWebhookServer(opts: WebhookServerOptions): http.Server {
  return http.createServer(async (req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    if (req.method === "GET" && url.pathname === "/healthz") {
      reply(res, 200, "ok");
      return;
    }
    if (url.pathname !== opts.path) {
      reply(res, 404, "not found");
      return;
    }
    if (req.method !== "POST") {
      res.setHeader("Allow", "POST");
      reply(res, 405, "method not allowed");
      return;
    }
    const contentType = req.headers["content-type"] ?? "";
    if (!contentType.startsWith("application/json")) {
      // A hook registered with the form content type signs the same bytes but
      // sends payload=<urlencoded>, which is not what the parser wants.
      reply(res, 415, "set the webhook content type to application/json");
      return;
    }

    let body: Buffer | null;
    try {
      body = await readBody(req, MAX_BODY_BYTES);
    } catch {
      reply(res, 400, "bad request");
      return;
    }
    if (body === null) {
      reply(res, 413, "payload too large");
      return;
    }

    const signature = req.headers["x-hub-signature-256"];
    if (!verifySignature(opts.secret, body, typeof signature === "string" ? signature : undefined)) {
      log("webhook: bad signature", { from: req.socket.remoteAddress });
      reply(res, 401, "bad signature");
      return;
    }

    const event = String(req.headers["x-github-event"] ?? "");
    const deliveryId = String(req.headers["x-github-delivery"] ?? "");
    if (event === "ping") {
      reply(res, 200, "pong");
      return;
    }

    let payload: unknown;
    try {
      payload = JSON.parse(body.toString("utf8"));
    } catch {
      reply(res, 400, "body is not JSON");
      return;
    }

    // Answering takes minutes; GitHub gives up after ten seconds.
    reply(res, 202, "accepted");
    opts.onDelivery(event, deliveryId, payload);
  });
}
