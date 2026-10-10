/**
 * The comment the bot posts.
 *
 * The answer is model output built from third-party text, so before it is
 * posted every HTML comment is stripped (the answered: marker below must not
 * be spoofable) and every @login is wrapped in backticks so the bot never
 * pings anyone.
 */

export const MARKER_PREFIX = "<!-- gh-helpbot answered:";

/** GitHub rejects comment bodies over 65536 characters; leave room for the footer. */
export const MAX_ANSWER_CHARS = 60_000;

export const DEFAULT_DOCS_URL = "https://github.com/mostlygeek/llama-swap/tree/main/docs";

export function marker(id: string): string {
  return `${MARKER_PREFIX}${id} -->`;
}

/** True when `body` is a reply the bot already posted for the comment or thread `id`. */
export function hasMarker(body: string | null | undefined, id: string): boolean {
  return Boolean(body) && (body as string).includes(marker(id));
}

export function sanitizeAnswer(answer: string): string {
  return answer
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/(^|[^\w`])@([A-Za-z0-9][\w-]*)/g, "$1`@$2`")
    .trim();
}

export interface ReplyMeta {
  /** The dedupe id this reply answers; goes into the marker. */
  id: string;
  model: string;
  mention: string;
  docsUrl?: string;
}

export function formatReply(answer: string, meta: ReplyMeta): string {
  let body = sanitizeAnswer(answer);
  if (body.length > MAX_ANSWER_CHARS) {
    body = body.slice(0, MAX_ANSWER_CHARS) + "\n\n_[answer truncated]_";
  }
  const docsUrl = meta.docsUrl ?? DEFAULT_DOCS_URL;
  return (
    `${body}\n\n---\n` +
    `<sub>Answered by gh-helpbot with llama-swap's Help agent (\`${meta.model}\`). ` +
    `It can be wrong; check the [documentation](${docsUrl}). ` +
    `Mention \`@${meta.mention}\` with a question to ask again.</sub>\n` +
    `${marker(meta.id)}\n`
  );
}
