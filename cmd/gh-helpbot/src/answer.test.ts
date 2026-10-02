import { describe, expect, it, vi } from "vitest";

import { buildQuestion, postReply } from "./answer";
import type { GitHubClient } from "./github";
import type { Job } from "./job";

function job(over: Partial<Job> = {}): Job {
  return {
    id: "IC_1",
    kind: "issue",
    repo: "o/r",
    number: 12,
    title: "Model unloads",
    threadBody: "It unloads after a minute.",
    question: "how do I keep it loaded?",
    subjectNodeId: "I_1",
    commentNodeId: "IC_1",
    htmlUrl: "",
    source: "test",
    ...over,
  };
}

describe("buildQuestion", () => {
  it("adds one line of thread context", () => {
    expect(buildQuestion(job())).toBe('how do I keep it loaded?\n\n(Asked on GitHub issue #12 "Model unloads" in o/r.)');
  });

  it("falls back to the thread when the mention had no question", () => {
    const q = buildQuestion(job({ question: "" }));
    expect(q.startsWith("Model unloads\n\nIt unloads after a minute.")).toBe(true);
  });

  it("bounds a very long opening post", () => {
    const q = buildQuestion(job({ question: "", threadBody: "x".repeat(20_000) }));
    expect(q.length).toBeLessThan(7000);
    expect(q).toContain("[...]");
  });
});

function fakeClient() {
  return {
    postIssueComment: vi.fn(async () => ({ url: "issue-url" })),
    postDiscussionComment: vi.fn(async () => ({ url: "disc-url" })),
    discussionThreadRoot: vi.fn(async () => "DC_root"),
  } as unknown as GitHubClient & {
    postIssueComment: ReturnType<typeof vi.fn>;
    postDiscussionComment: ReturnType<typeof vi.fn>;
    discussionThreadRoot: ReturnType<typeof vi.fn>;
  };
}

describe("postReply", () => {
  it("comments on issues by number", async () => {
    const client = fakeClient();
    expect(await postReply(client, job(), "body")).toEqual({ url: "issue-url" });
    expect(client.postIssueComment).toHaveBeenCalledWith("o/r", 12, "body");
  });

  it("replies under a top-level discussion comment directly", async () => {
    const client = fakeClient();
    await postReply(client, job({ kind: "discussion", subjectNodeId: "D_1", commentNodeId: "DC_5" }), "body");
    expect(client.postDiscussionComment).toHaveBeenCalledWith("D_1", "body", "DC_5");
    expect(client.discussionThreadRoot).not.toHaveBeenCalled();
  });

  it("looks up the thread root only for a webhook reply with a parent", async () => {
    const client = fakeClient();
    await postReply(client, job({ kind: "discussion", subjectNodeId: "D_1", commentNodeId: "DC_5", parentCommentDbId: 77 }), "body");
    expect(client.discussionThreadRoot).toHaveBeenCalledWith("DC_5");
    expect(client.postDiscussionComment).toHaveBeenCalledWith("D_1", "body", "DC_root");
  });

  it("uses the parent the poller already knows", async () => {
    const client = fakeClient();
    await postReply(client, job({ kind: "discussion", subjectNodeId: "D_1", commentNodeId: "DC_5", parentCommentNodeId: "DC_top" }), "body");
    expect(client.postDiscussionComment).toHaveBeenCalledWith("D_1", "body", "DC_top");
  });

  it("posts a top-level comment for a mention in the discussion body", async () => {
    const client = fakeClient();
    await postReply(client, job({ kind: "discussion", subjectNodeId: "D_1", commentNodeId: undefined }), "body");
    expect(client.postDiscussionComment).toHaveBeenCalledWith("D_1", "body", undefined);
  });
});
