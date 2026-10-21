import { describe, expect, it } from "vitest";

import type { ThreadSnapshot } from "./github";
import { collectJobs } from "./poll";
import { marker } from "./reply";

const SINCE = "2026-09-18T10:00:00.000Z";
const OLD = "2026-09-18T09:00:00.000Z";
const NEW = "2026-09-18T11:00:00.000Z";

const user = { login: "someone", isBot: false };

function issue(over: Partial<ThreadSnapshot> = {}): ThreadSnapshot {
  return {
    kind: "issue",
    repo: "o/r",
    id: "I_1",
    number: 1,
    title: "Title",
    body: "opening post",
    updatedAt: NEW,
    url: "https://x/1",
    author: user,
    comments: [],
    ...over,
  };
}

const opts = { since: SINCE, mention: "help", selfLogin: "helpbot", handled: new Set<string>() };

describe("collectJobs", () => {
  it("finds a new comment mention and keys it by the comment id", () => {
    const jobs = collectJobs(
      [issue({ comments: [{ id: "IC_1", body: "@help what is ttl", updatedAt: NEW, url: "https://x/1#c1", author: user }] })],
      opts,
    );
    expect(jobs).toMatchObject([
      { id: "IC_1", kind: "issue", number: 1, question: "what is ttl", commentNodeId: "IC_1", subjectNodeId: "I_1", htmlUrl: "https://x/1#c1", source: "poll" },
    ]);
  });

  it("finds a mention in an opening post without a comment id", () => {
    const jobs = collectJobs([issue({ body: "@help how do groups work" })], opts);
    expect(jobs).toMatchObject([{ id: "I_1", question: "how do groups work" }]);
    expect(jobs[0].commentNodeId).toBeUndefined();
  });

  it("skips comments older than since, by the bot, by bots, or already handled", () => {
    const thread = issue({
      comments: [
        { id: "IC_old", body: "@help old", updatedAt: OLD, url: "", author: user },
        { id: "IC_self", body: "@help self", updatedAt: NEW, url: "", author: { login: "helpbot", isBot: false } },
        { id: "IC_bot", body: "@help bot", updatedAt: NEW, url: "", author: { login: "x[bot]", isBot: true } },
        { id: "IC_done", body: "@help done", updatedAt: NEW, url: "", author: user },
        { id: "IC_new", body: "@help new", updatedAt: NEW, url: "", author: user },
      ],
    });
    const jobs = collectJobs([thread], { ...opts, handled: new Set(["IC_done"]) });
    expect(jobs.map((j) => j.id)).toEqual(["IC_new"]);
  });

  it("skips a mention that already has a marked reply in the thread", () => {
    const thread = issue({
      body: "@help in body",
      comments: [
        { id: "IC_1", body: "@help asked", updatedAt: NEW, url: "", author: user },
        { id: "IC_2", body: `answer\n${marker("IC_1")}`, updatedAt: NEW, url: "", author: { login: "helpbot", isBot: false } },
        { id: "IC_3", body: `answer\n${marker("I_1")}`, updatedAt: NEW, url: "", author: { login: "helpbot", isBot: false } },
      ],
    });
    expect(collectJobs([thread], opts)).toEqual([]);
  });

  it("threads discussion replies under their top-level comment", () => {
    const thread: ThreadSnapshot = {
      ...issue(),
      kind: "discussion",
      id: "D_1",
      comments: [
        {
          id: "DC_top",
          body: "first",
          updatedAt: OLD,
          url: "",
          author: user,
          replies: [
            { id: "DC_reply", body: "@help in a reply", updatedAt: NEW, url: "https://x/d#r", author: user },
            { id: "DC_answer", body: `x ${marker("DC_other")}`, updatedAt: NEW, url: "", author: user },
          ],
        },
      ],
    };
    expect(collectJobs([thread], opts)).toMatchObject([
      { id: "DC_reply", kind: "discussion", commentNodeId: "DC_reply", parentCommentNodeId: "DC_top", subjectNodeId: "D_1" },
    ]);
  });
});
