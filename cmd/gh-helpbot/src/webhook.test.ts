import { createHmac } from "node:crypto";
import { readFile } from "node:fs/promises";
import type { AddressInfo } from "node:net";
import { afterEach, describe, expect, it } from "vitest";

import { createWebhookServer, parseEvent, verifySignature, MAX_BODY_BYTES } from "./webhook";

async function payload(name: string): Promise<any> {
  return JSON.parse(await readFile(new URL(`../testdata/${name}.json`, import.meta.url), "utf8"));
}

const opts = { mention: "help", selfLogin: "helpbot-account", source: "d1" };

describe("verifySignature", () => {
  const secret = "s3cret";
  const body = Buffer.from('{"a":1}');
  const good = "sha256=" + createHmac("sha256", secret).update(body).digest("hex");

  it("accepts the right signature and rejects everything else", () => {
    expect(verifySignature(secret, body, good)).toBe(true);
    expect(verifySignature(secret, body, good.replace(/.$/, (c) => (c === "0" ? "1" : "0")))).toBe(false);
    expect(verifySignature(secret, body, "sha256=abc")).toBe(false);
    expect(verifySignature(secret, body, "sha1=" + good.slice(7))).toBe(false);
    expect(verifySignature(secret, body, undefined)).toBe(false);
    expect(verifySignature("other", body, good)).toBe(false);
  });
});

describe("parseEvent", () => {
  it("turns an issue comment into a job", async () => {
    const result = parseEvent("issue_comment", await payload("issue_comment"), opts);
    expect(result).toMatchObject({
      job: {
        id: "IC_kwDOAAAAAB",
        kind: "issue",
        repo: "mostlygeek/llama-swap",
        number: 42,
        title: "Model unloads too early",
        question: "how do I make a model unload after 5 minutes instead?",
        subjectNodeId: "I_kwDOAAAAAA",
        commentNodeId: "IC_kwDOAAAAAB",
        htmlUrl: "https://github.com/mostlygeek/llama-swap/issues/42#issuecomment-1",
        source: "d1",
      },
    });
    expect((result as any).job.threadBody).toContain("ttl: 60");
  });

  it("skips pull request comments", async () => {
    expect(parseEvent("issue_comment", await payload("issue_comment_pr"), opts)).toEqual({ skip: "comment on a pull request" });
  });

  it("answers a mention in a new issue's body", async () => {
    const result = parseEvent("issues", await payload("issues_opened"), opts);
    expect(result).toMatchObject({ job: { id: "I_kwDOAAAAAE", kind: "issue", number: 44 } });
    expect((result as any).job.commentNodeId).toBeUndefined();
    expect((result as any).job.question).toContain("two GPUs");
  });

  it("carries the numeric parent id of a discussion reply", async () => {
    const result = parseEvent("discussion_comment", await payload("discussion_comment"), opts);
    expect(result).toMatchObject({
      job: {
        id: "DC_kwDOAAAAAG",
        kind: "discussion",
        number: 7,
        subjectNodeId: "D_kwDOAAAAAF",
        commentNodeId: "DC_kwDOAAAAAG",
        parentCommentDbId: 9001,
      },
    });
  });

  it("uses the thread as the question when a discussion body is only the mention", async () => {
    const result = parseEvent("discussion", await payload("discussion_created"), opts);
    expect(result).toMatchObject({ job: { id: "D_kwDOAAAAAH", kind: "discussion", question: "" } });
  });

  it("skips its own comments, bots, other actions, other repos and other events", async () => {
    const base = await payload("issue_comment");
    const mine = structuredClone(base);
    mine.comment.user.login = "helpbot-account";
    expect(parseEvent("issue_comment", mine, opts)).toEqual({ skip: "author is the bot itself" });

    const bot = structuredClone(base);
    bot.comment.user = { login: "github-actions[bot]", type: "Bot" };
    expect(parseEvent("issue_comment", bot, opts)).toEqual({ skip: "author github-actions[bot] is a bot" });

    const edited = structuredClone(base);
    edited.action = "edited";
    expect(parseEvent("issue_comment", edited, opts)).toEqual({ skip: "issue_comment edited" });

    expect(parseEvent("issue_comment", base, { ...opts, repos: ["other/repo"] })).toEqual({
      skip: "repository mostlygeek/llama-swap is not allowed",
    });
    expect(parseEvent("issue_comment", base, { ...opts, repos: ["mostlygeek/llama-swap"] })).toHaveProperty("job");

    const quiet = structuredClone(base);
    quiet.comment.body = "no mention here";
    expect(parseEvent("issue_comment", quiet, opts)).toEqual({ skip: "no mention" });

    expect(parseEvent("push", { repository: { full_name: "a/b" } }, opts)).toEqual({ skip: "unhandled event push" });
    expect(parseEvent("issue_comment", {}, opts)).toEqual({ skip: "payload has no repository" });
  });
});

describe("createWebhookServer", () => {
  const secret = "hook-secret";
  const deliveries: Array<{ event: string; id: string; payload: any }> = [];
  const server = createWebhookServer({
    secret,
    path: "/webhook",
    onDelivery: (event, id, payload) => deliveries.push({ event, id, payload }),
  });
  let base = "";

  async function listen(): Promise<string> {
    if (base) return base;
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    return base;
  }

  afterEach(() => {
    deliveries.length = 0;
  });

  function post(path: string, body: string, headers: Record<string, string>) {
    return fetch(`${base}${path}`, { method: "POST", body, headers });
  }

  function signed(body: string, event: string, extra: Record<string, string> = {}): Record<string, string> {
    return {
      "content-type": "application/json",
      "x-hub-signature-256": "sha256=" + createHmac("sha256", secret).update(body).digest("hex"),
      "x-github-event": event,
      "x-github-delivery": "deliv-1",
      ...extra,
    };
  }

  it("answers healthz and rejects the wrong path or method", async () => {
    await listen();
    expect((await fetch(`${base}/healthz`)).status).toBe(200);
    expect((await fetch(`${base}/webhook`)).status).toBe(405);
    expect((await post("/elsewhere", "{}", signed("{}", "ping"))).status).toBe(404);
  });

  it("requires JSON, a valid signature and a small body", async () => {
    await listen();
    const body = '{"zen":"x"}';
    expect((await post("/webhook", body, { ...signed(body, "ping"), "content-type": "application/x-www-form-urlencoded" })).status).toBe(415);
    expect((await post("/webhook", body, { ...signed(body, "ping"), "x-hub-signature-256": "sha256=00" })).status).toBe(401);
    expect((await post("/webhook", body, { "content-type": "application/json" })).status).toBe(401);
    const big = "x".repeat(MAX_BODY_BYTES + 1);
    const res = await post("/webhook", big, signed(big, "ping")).catch(() => undefined);
    if (res) expect(res.status).toBe(413);
    expect(deliveries).toEqual([]);
  });

  it("pongs a ping and accepts a delivery", async () => {
    await listen();
    expect((await post("/webhook", '{"zen":"x"}', signed('{"zen":"x"}', "ping"))).status).toBe(200);
    const body = JSON.stringify({ action: "created" });
    const res = await post("/webhook", body, signed(body, "issue_comment"));
    expect(res.status).toBe(202);
    expect(deliveries).toEqual([{ event: "issue_comment", id: "deliv-1", payload: { action: "created" } }]);
    const bad = await post("/webhook", "{not json", signed("{not json", "issue_comment"));
    expect(bad.status).toBe(400);
    server.close();
  });
});
