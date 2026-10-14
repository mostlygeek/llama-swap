import { afterEach, describe, expect, it, vi } from "vitest";

import { GitHubClient, GitHubError } from "./github";

type Call = { url: string; init: RequestInit };

function stub(responses: Array<{ status?: number; body?: unknown; headers?: Record<string, string> }>): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      const next = responses.shift() ?? { status: 200, body: {} };
      const status = next.status ?? 200;
      return new Response(JSON.stringify(next.body ?? {}), {
        status,
        headers: { "content-type": "application/json", ...(next.headers ?? {}) },
      });
    }),
  );
  return calls;
}

afterEach(() => vi.unstubAllGlobals());

const client = () => new GitHubClient({ token: "tok", sleep: async () => {} });

describe("GitHubClient", () => {
  it("sends the token and posts issue comments over REST", async () => {
    const calls = stub([{ status: 201, body: { html_url: "https://gh/c/1" } }]);
    const result = await client().postIssueComment("o/r", 5, "hello");
    expect(result).toEqual({ url: "https://gh/c/1" });
    expect(calls[0].url).toBe("https://api.github.com/repos/o/r/issues/5/comments");
    expect(new Headers(calls[0].init.headers).get("authorization")).toBe("Bearer tok");
    expect(JSON.parse(calls[0].init.body as string)).toEqual({ body: "hello" });
  });

  it("posts discussion replies through GraphQL", async () => {
    const calls = stub([{ body: { data: { addDiscussionComment: { comment: { id: "DC_9", url: "https://gh/d/9" } } } } }]);
    const result = await client().postDiscussionComment("D_1", "hi", "DC_top");
    expect(result).toEqual({ url: "https://gh/d/9" });
    const sent = JSON.parse(calls[0].init.body as string);
    expect(calls[0].url).toBe("https://api.github.com/graphql");
    expect(sent.query).toContain("addDiscussionComment");
    expect(sent.variables).toEqual({ discussionId: "D_1", body: "hi", replyToId: "DC_top" });
  });

  it("resolves a reply's top-level comment", async () => {
    stub([{ body: { data: { node: { replyTo: { id: "DC_root" } } } } }]);
    expect(await client().discussionThreadRoot("DC_leaf")).toBe("DC_root");
    stub([{ body: { data: { node: { replyTo: null } } } }]);
    expect(await client().discussionThreadRoot("DC_top")).toBe("DC_top");
  });

  it("retries 5xx and network errors, then gives up", async () => {
    const calls = stub([{ status: 502 }, { status: 503 }, { status: 200, body: { data: { viewer: { login: "me" } } } }]);
    expect(await client().viewerLogin()).toBe("me");
    expect(calls).toHaveLength(3);

    stub([{ status: 500 }, { status: 500 }, { status: 500 }]);
    await expect(client().viewerLogin()).rejects.toBeInstanceOf(GitHubError);
  });

  it("does not retry 4xx and reports rate limits", async () => {
    const calls = stub([{ status: 403, body: { message: "slow down" }, headers: { "retry-after": "60", "x-ratelimit-remaining": "0" } }]);
    await expect(client().postIssueComment("o/r", 1, "x")).rejects.toThrow(/rate limited/);
    expect(calls).toHaveLength(1);
  });

  it("surfaces GraphQL errors", async () => {
    stub([{ body: { errors: [{ message: "Resource not accessible by integration" }] } }]);
    await expect(client().viewerLogin()).rejects.toThrow(/Resource not accessible/);
  });

  it("pages recent threads until they are older than since, and tolerates disabled discussions", async () => {
    const page = (nodes: unknown[], hasNextPage: boolean, key: "issues" | "discussions") => ({
      body: { data: { repository: { [key]: { pageInfo: { hasNextPage, endCursor: "c" }, nodes } } } },
    });
    const thread = (id: string, updatedAt: string) => ({
      id,
      number: 1,
      title: "t",
      body: null,
      updatedAt,
      url: "u",
      author: { login: "a", __typename: "User" },
      comments: { nodes: [{ id: `${id}_c`, body: "b", updatedAt, url: "u", author: null }] },
    });
    const calls = stub([
      page([thread("I_new", "2026-09-18T12:00:00Z")], true, "issues"),
      page([thread("I_old", "2026-09-18T01:00:00Z")], true, "issues"),
      { body: { data: { repository: { discussions: null } }, errors: [{ message: "Discussions are disabled" }] } },
    ]);
    const threads = await client().recentThreads("o/r", "2026-09-18T10:00:00Z");
    expect(threads.map((t) => t.id)).toEqual(["I_new"]);
    expect(threads[0].body).toBe("");
    expect(threads[0].comments[0].author).toBeNull();
    expect(calls).toHaveLength(3);
    expect(JSON.parse(calls[1].init.body as string).variables.after).toBe("c");
  });
});
