/**
 * Detecting the bot's mention in a comment.
 *
 * Detection runs over the comment with quoted lines and fenced code removed:
 * an email reply quotes the previous comment, and someone quoting the bot's
 * own answer would otherwise trigger it again. The query keeps the code
 * blocks, because a pasted config is usually the useful part of a question.
 */

export const DEFAULT_MENTION = "help";

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** `@help` preceded by start or whitespace and not followed by a login character, so `@helper` and `@help-desk` do not match. */
export function mentionPattern(mention: string): RegExp {
  return new RegExp(`(^|\\s)@${escapeRegExp(mention)}(?![\\w-])`, "gi");
}

/** Removes fenced code blocks and `>` quoted lines. Exported for tests. */
export function stripQuotesAndCode(body: string): string {
  return body
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/~~~[\s\S]*?~~~/g, " ")
    .split("\n")
    .filter((line) => !/^\s*>/.test(line))
    .join("\n");
}

export interface MentionResult {
  found: boolean;
  /** The body with every mention removed and whitespace tidied. Empty when the comment was only the mention. */
  query: string;
}

export function findMention(body: string | null | undefined, mention: string = DEFAULT_MENTION): MentionResult {
  if (!body) return { found: false, query: "" };
  const found = mentionPattern(mention).test(stripQuotesAndCode(body));
  if (!found) return { found: false, query: "" };
  const query = body
    .replace(mentionPattern(mention), "$1")
    .replace(/[ \t]+/g, " ")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
  return { found: true, query };
}
