import { log } from "./log";
import type { ThreadKind } from "./job";

/**
 * The slice of GitHub's API the bot needs, over fetch with no SDK.
 *
 * Issues are commented on through REST; discussions only exist in GraphQL.
 * The poller reads both through GraphQL because that is the one API that
 * returns discussion comments and their replies.
 */

export const DEFAULT_API_URL = "https://api.github.com";

export class GitHubError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message);
    this.name = "GitHubError";
  }
}

export interface Author {
  login: string;
  isBot: boolean;
}

export interface SnapshotComment {
  id: string;
  body: string;
  updatedAt: string;
  url: string;
  author: Author | null;
  /** Discussions only: one level of threaded replies. */
  replies?: SnapshotComment[];
}

export interface ThreadSnapshot {
  kind: ThreadKind;
  repo: string;
  id: string;
  number: number;
  title: string;
  body: string;
  updatedAt: string;
  url: string;
  author: Author | null;
  comments: SnapshotComment[];
}

export interface GitHubClientOptions {
  token: string;
  apiUrl?: string;
  /** Attempts per request on network errors and 5xx. */
  tries?: number;
  sleep?: (ms: number) => Promise<void>;
}

interface GraphQLResponse<T> {
  data?: T;
  errors?: Array<{ message: string; type?: string; path?: unknown[] }>;
}

const RETRY_BASE_MS = 1000;

function isRetryable(status: number): boolean {
  return status >= 500 && status < 600;
}

export class GitHubClient {
  private readonly apiUrl: string;
  private readonly tries: number;
  private readonly sleep: (ms: number) => Promise<void>;

  constructor(private readonly opts: GitHubClientOptions) {
    this.apiUrl = (opts.apiUrl ?? DEFAULT_API_URL).replace(/\/+$/, "");
    this.tries = opts.tries ?? 3;
    this.sleep = opts.sleep ?? ((ms) => new Promise((resolve) => setTimeout(resolve, ms)));
  }

  private headers(): Record<string, string> {
    return {
      Authorization: `Bearer ${this.opts.token}`,
      Accept: "application/vnd.github+json",
      "Content-Type": "application/json",
      "User-Agent": "gh-helpbot",
      "X-GitHub-Api-Version": "2022-11-28",
    };
  }

  /** POSTs JSON, retrying network errors and 5xx with backoff. Other failures throw at once. */
  private async post(url: string, body: unknown): Promise<Response> {
    let lastError: unknown;
    for (let attempt = 1; attempt <= this.tries; attempt++) {
      try {
        const response = await fetch(url, {
          method: "POST",
          headers: this.headers(),
          body: JSON.stringify(body),
        });
        if (!isRetryable(response.status)) return response;
        lastError = new GitHubError(`${response.status} from ${url}`, response.status);
      } catch (error) {
        lastError = error;
      }
      if (attempt < this.tries) await this.sleep(RETRY_BASE_MS * 2 ** (attempt - 1));
    }
    throw lastError instanceof Error ? lastError : new GitHubError(String(lastError));
  }

  private async failure(response: Response, what: string): Promise<GitHubError> {
    const text = await response.text();
    const remaining = response.headers.get("x-ratelimit-remaining");
    const retryAfter = response.headers.get("retry-after");
    if (response.status === 403 && (remaining === "0" || retryAfter)) {
      return new GitHubError(
        `${what}: rate limited (retry-after ${retryAfter ?? "unknown"}, remaining ${remaining ?? "unknown"})`,
        response.status,
      );
    }
    return new GitHubError(`${what}: ${response.status} ${text.slice(0, 300)}`, response.status);
  }

  async graphql<T>(query: string, variables: Record<string, unknown> = {}): Promise<T> {
    const response = await this.post(`${this.apiUrl}/graphql`, { query, variables });
    if (!response.ok) throw await this.failure(response, "graphql");
    const body = (await response.json()) as GraphQLResponse<T>;
    if (body.errors?.length) {
      throw new GitHubError(`graphql: ${body.errors.map((e) => e.message).join("; ")}`, response.status);
    }
    if (!body.data) throw new GitHubError("graphql: empty response");
    return body.data;
  }

  /** Same as graphql() but hands back partial data alongside errors, for queries where one field may be disabled. */
  async graphqlPartial<T>(query: string, variables: Record<string, unknown> = {}): Promise<GraphQLResponse<T>> {
    const response = await this.post(`${this.apiUrl}/graphql`, { query, variables });
    if (!response.ok) throw await this.failure(response, "graphql");
    return (await response.json()) as GraphQLResponse<T>;
  }

  /** The token's own login. Throws for tokens that cannot ask (the Actions token). */
  async viewerLogin(): Promise<string> {
    const data = await this.graphql<{ viewer: { login: string } }>("query { viewer { login } }");
    return data.viewer.login;
  }

  async postIssueComment(repo: string, number: number, body: string): Promise<{ url: string }> {
    const response = await this.post(`${this.apiUrl}/repos/${repo}/issues/${number}/comments`, { body });
    if (!response.ok) throw await this.failure(response, `comment on ${repo}#${number}`);
    const json = (await response.json()) as { html_url?: string };
    return { url: json.html_url ?? "" };
  }

  async postDiscussionComment(discussionId: string, body: string, replyToId?: string): Promise<{ url: string }> {
    const data = await this.graphql<{ addDiscussionComment: { comment: { url: string } } }>(
      `mutation($discussionId: ID!, $body: String!, $replyToId: ID) {
        addDiscussionComment(input: { discussionId: $discussionId, body: $body, replyToId: $replyToId }) {
          comment { id url }
        }
      }`,
      { discussionId, body, replyToId: replyToId ?? null },
    );
    return { url: data.addDiscussionComment.comment.url };
  }

  /**
   * The top-level comment a discussion comment belongs to. Threads are one
   * level deep, so a reply to a reply goes under the same parent.
   */
  async discussionThreadRoot(commentNodeId: string): Promise<string> {
    const data = await this.graphql<{ node: { replyTo?: { id: string } | null } | null }>(
      `query($id: ID!) { node(id: $id) { ... on DiscussionComment { replyTo { id } } } }`,
      { id: commentNodeId },
    );
    return data.node?.replyTo?.id ?? commentNodeId;
  }

  /**
   * Issues and discussions updated at or after `since`, newest first, with
   * their comments. Pages until it reaches a thread older than `since`.
   */
  async recentThreads(repo: string, since: string, opts: { maxPages?: number } = {}): Promise<ThreadSnapshot[]> {
    const [owner, name] = repo.split("/");
    if (!owner || !name) throw new GitHubError(`bad repo ${JSON.stringify(repo)}; want owner/name`);
    const maxPages = opts.maxPages ?? 5;
    const sinceMs = Date.parse(since);

    const issues = await this.pageThreads(
      "issue",
      repo,
      sinceMs,
      maxPages,
      ISSUES_QUERY,
      { owner, name },
      (data: any) => data?.repository?.issues,
    );
    const discussions = await this.pageThreads(
      "discussion",
      repo,
      sinceMs,
      maxPages,
      DISCUSSIONS_QUERY,
      { owner, name },
      (data: any) => data?.repository?.discussions,
    );
    return [...issues, ...discussions];
  }

  private async pageThreads(
    kind: ThreadKind,
    repo: string,
    sinceMs: number,
    maxPages: number,
    query: string,
    variables: Record<string, unknown>,
    pick: (data: unknown) => { pageInfo: { hasNextPage: boolean; endCursor: string | null }; nodes: RawThread[] } | undefined,
  ): Promise<ThreadSnapshot[]> {
    const out: ThreadSnapshot[] = [];
    let after: string | null = null;
    for (let page = 0; page < maxPages; page++) {
      const result = await this.graphqlPartial<unknown>(query, { ...variables, after });
      const connection = pick(result.data);
      if (!connection) {
        // Discussions can be switched off per repository; that is a GraphQL
        // error with null data for the field, not a transport failure.
        const reason = result.errors?.map((e) => e.message).join("; ") ?? "no data";
        if (kind === "discussion") {
          log("poll: discussions unavailable", { repo, reason });
          return out;
        }
        throw new GitHubError(`graphql: ${reason}`);
      }
      let reachedOlder = false;
      for (const node of connection.nodes) {
        if (Date.parse(node.updatedAt) < sinceMs) {
          reachedOlder = true;
          break;
        }
        out.push(toSnapshot(kind, repo, node));
      }
      if (reachedOlder || !connection.pageInfo.hasNextPage) break;
      after = connection.pageInfo.endCursor;
    }
    return out;
  }
}

interface RawAuthor {
  login: string;
  __typename: string;
}

interface RawComment {
  id: string;
  body: string | null;
  updatedAt: string;
  url: string;
  author: RawAuthor | null;
  replies?: { nodes: RawComment[] };
}

interface RawThread {
  id: string;
  number: number;
  title: string;
  body: string | null;
  updatedAt: string;
  url: string;
  author: RawAuthor | null;
  comments: { nodes: RawComment[] };
}

export function toAuthor(raw: RawAuthor | null): Author | null {
  if (!raw) return null;
  return { login: raw.login, isBot: raw.__typename === "Bot" };
}

function toComment(raw: RawComment): SnapshotComment {
  const comment: SnapshotComment = {
    id: raw.id,
    body: raw.body ?? "",
    updatedAt: raw.updatedAt,
    url: raw.url,
    author: toAuthor(raw.author),
  };
  if (raw.replies) comment.replies = raw.replies.nodes.map(toComment);
  return comment;
}

export function toSnapshot(kind: ThreadKind, repo: string, raw: RawThread): ThreadSnapshot {
  return {
    kind,
    repo,
    id: raw.id,
    number: raw.number,
    title: raw.title,
    body: raw.body ?? "",
    updatedAt: raw.updatedAt,
    url: raw.url,
    author: toAuthor(raw.author),
    comments: raw.comments.nodes.map(toComment),
  };
}

const AUTHOR = "author { login __typename }";
const COMMENT_FIELDS = `id body updatedAt url ${AUTHOR}`;

export const ISSUES_QUERY = `query($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    issues(first: 30, after: $after, orderBy: { field: UPDATED_AT, direction: DESC }) {
      pageInfo { hasNextPage endCursor }
      nodes {
        id number title body updatedAt url ${AUTHOR}
        comments(last: 50) { nodes { ${COMMENT_FIELDS} } }
      }
    }
  }
}`;

export const DISCUSSIONS_QUERY = `query($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    discussions(first: 30, after: $after, orderBy: { field: UPDATED_AT, direction: DESC }) {
      pageInfo { hasNextPage endCursor }
      nodes {
        id number title body updatedAt url ${AUTHOR}
        comments(last: 50) {
          nodes {
            ${COMMENT_FIELDS}
            replies(last: 50) { nodes { ${COMMENT_FIELDS} } }
          }
        }
      }
    }
  }
}`;
