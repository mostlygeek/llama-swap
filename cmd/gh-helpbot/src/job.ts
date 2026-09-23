export type ThreadKind = "issue" | "discussion";

/**
 * One @mention to answer. Every trigger mode (webhook, poll, action, replay)
 * reduces what it sees to this shape, so answering and posting are written
 * once.
 */
export interface Job {
  /** Dedupe key: the node id of the triggering comment, or of the thread when the mention is in its opening post. */
  id: string;
  kind: ThreadKind;
  /** owner/name */
  repo: string;
  number: number;
  title: string;
  /** The opening post of the issue or discussion. */
  threadBody: string;
  /** The mention's text with the mention itself removed. Empty means "the thread is the question". */
  question: string;
  /** Node id of the issue or discussion, needed to post a discussion reply. */
  subjectNodeId: string;
  /** Node id of the triggering comment; absent when the mention was in the opening post. */
  commentNodeId?: string;
  /** Discussions: the top-level comment to reply under when the trigger was itself a reply. */
  parentCommentNodeId?: string;
  /** Discussions, from a webhook: the parent's numeric id. Means "look the node id up before replying". */
  parentCommentDbId?: number;
  htmlUrl: string;
  /** Where the job came from: a webhook delivery id, "poll", "action" or "replay". */
  source: string;
}
