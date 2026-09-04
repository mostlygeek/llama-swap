/**
 * The questions offered on the empty Help page.
 *
 * These are the first thing a new user reads, so they do double duty: they
 * show that the agent answers questions about *this* server, and they name
 * features most people never find. Someone who does not know llama-swap can
 * run several models at once, or resolve one model ID to a different model per
 * request, will never think to ask -- so the list asks for them.
 *
 * Two rules for editing:
 *
 *   1. Every question must be answerable from `docs/kb/` or the running
 *      config. A suggestion the agent cannot answer teaches the reader that
 *      Help does not work, which is worse than offering nothing. The comment
 *      above each group names the article that answers it.
 *   2. Keep them short enough to read at a glance -- roughly two lines in the
 *      button at a narrow width.
 *   3. Add new questions at the end. Each one is shown with its position in
 *      this list ("#12."), so inserting in the middle renumbers everything
 *      after it and the number someone wrote down stops meaning what it did.
 */
export const DOCS_SUGGESTIONS: string[] = [
  // The running config, read through config__get_config.
  "What llama-swap features am I not using?",
  "What models are configured on this server?",
  "Which of my models never unload, and how much are they holding?",

  // guides/routing/groups-and-matrix, guides/routing/profiles-and-selectors
  "How do I run several models at once instead of swapping between them?",
  "Can I switch between whole sets of models at runtime?",
  "Can one model name resolve to a different model per request?",

  // guides/model-runtime/ttl-and-unloading, .../troubleshooting-model-wont-load
  "How do I unload a model after 5 minutes of inactivity?",
  "My model won't load. How do I debug it?",

  // guides/connectivity/remote-peers, examples/peers-and-multi-host
  "How do I serve models running on another machine through this one?",

  // examples/speculative-decoding
  "How do I use a small draft model to speed up a large one?",

  // guides/operations/startup-preloading-and-hooks
  "How do I load my most-used models at startup instead of on the first request?",

  // guides/routing/capacity-and-queues
  "How do I limit how many requests one model handles at a time?",

  // guides/api-integration/filters-and-request-rewriting
  "How do I stop clients from overriding a sampling parameter?",

  // guides/configuration/macros
  "How do I stop repeating the same long command in every model?",

  // guides/api-integration/api-keys-and-auth
  "How do I keep API keys out of my config file?",

  // guides/model-runtime/capabilities-and-model-listings
  "How do I tell clients which of my models handle images or tool calls?",

  // guides/model-runtime/client-compatibility-and-loading-feedback
  "My client errors out while a model is still loading. Can I fix that?",

  // guides/operations/observability-storage-and-activity
  "Can I see the full request and response body for a call?",

  // guides/api-integration/mcp-endpoint
  "How do I point an MCP client at this server?",

  // guides/operations/tailcat
  "How do I reach this server from another network without opening a port?",

  // guides/operations/container-security
  "How do I run llama-swap in a container without running as root?",
];

/** How many suggestions the Help page shows at once. */
export const SUGGESTION_COUNT = 4;

/** One question, with the number it is shown under. */
export interface DocsSuggestion {
  /** Its position in the pool, from 1. Shown to the reader as "#12.". */
  number: number;
  question: string;
}

/**
 * The pool, numbered. The number is the question's place in the list, so it
 * names a specific topic rather than a slot on screen: four are shown at a
 * time, and "#12" is the same question every time it comes up.
 */
export const NUMBERED_SUGGESTIONS: DocsSuggestion[] = DOCS_SUGGESTIONS.map((question, index) => ({
  number: index + 1,
  question,
}));

/**
 * Draws suggestions at random, without repeats, in ascending order.
 *
 * The draw is what makes the list a tour: a reader who comes back, or clears
 * the chat, is shown a different corner of llama-swap rather than the same
 * four questions they already decided they did not need. The order is not part
 * of that -- four numbers going up read as a list, the same four shuffled read
 * as a mistake -- so the randomness picks which questions, not where they sit.
 */
export function pickSuggestions(
  count: number = SUGGESTION_COUNT,
  pool: DocsSuggestion[] = NUMBERED_SUGGESTIONS,
): DocsSuggestion[] {
  const shuffled = [...pool];
  // Fisher-Yates. Sorting by a random comparator is the tempting one-liner and
  // it is not a uniform shuffle.
  for (let i = shuffled.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
  }
  return shuffled.slice(0, Math.max(0, count)).sort((a, b) => a.number - b.number);
}
