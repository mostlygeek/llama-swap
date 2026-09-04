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
  "What can llama-swap do that I'm not using?",
  "What models are set up here?",
  "Which models stay loaded, and how much memory do they use?",

  // guides/routing/groups-and-matrix, guides/routing/profiles-and-selectors
  "How can I keep several models ready at once?",
  "How can I switch between sets of models?",
  "Can one model name choose a different model each time?",

  // guides/model-runtime/ttl-and-unloading, .../troubleshooting-model-wont-load
  "How do I unload a model after five minutes idle?",
  "A model won't start. How do I fix it?",

  // guides/connectivity/remote-peers, examples/peers-and-multi-host
  "How do I use models on another computer?",

  // guides/operations/startup-preloading-and-hooks
  "How do I load my most-used models at startup?",

  // guides/routing/capacity-and-queues
  "How many requests can one model handle at once?",

  // guides/api-integration/filters-and-request-rewriting
  "How do I stop apps changing a model's settings?",

  // guides/configuration/macros
  "How do I avoid repeating a long command for every model?",

  // guides/api-integration/api-keys-and-auth
  "How do I keep API keys out of my config file?",

  // guides/model-runtime/capabilities-and-model-listings
  "How do I tell apps a model can handle images or tools?",

  // guides/model-runtime/client-compatibility-and-loading-feedback
  "My app fails while a model loads. What can I do?",

  // guides/operations/observability-storage-and-activity
  "Can I save requests and responses while I troubleshoot?",

  // guides/api-integration/mcp-endpoint
  "How do I connect an MCP app to this server?",

  // guides/operations/tailcat
  "How do I reach this server safely from another network?",

  // guides/operations/container-security
  "How do I run llama-swap more safely in a container?",

  // guides/model-runtime/writing-cmd
  "What is the smallest model setup to get started?",
  "My model starts but does not answer. What is wrong?",
  "Can I use a server other than llama-server?",
  "How do I set up tool use in llama-server?",
  "How do I make a model use certain GPUs?",
  "How do I send a different model name to the server behind llama-swap?",

  // guides/model-runtime/ttl-and-unloading
  "How does automatic model unloading work?",
  "How do I fully stop a Docker model container?",
  "How do I unload a model right now?",
  "Which models should always stay loaded?",

  // guides/model-runtime/capabilities-and-model-listings,
  // guides/model-runtime/client-compatibility-and-loading-feedback
  "Why does my model say it supports tools but not use them?",
  "How do I show model nicknames to apps?",
  "How can an app show that a model is still loading?",

  // guides/model-runtime/troubleshooting-model-wont-load,
  // guides/connectivity/proxy-timeouts
  "How do I make sure llama-swap knows when a model is ready?",
  "Where can I find why a model did not start?",
  "A model takes forever to start. Should I just wait longer?",

  // guides/routing/groups-and-matrix
  "How should I arrange models that can run together?",
  "How do I keep embeddings ready while swapping chat models?",
  "How do I stop a slow model being unloaded too soon?",

  // guides/routing/capacity-and-queues
  "How do I limit a model's requests at one time?",
  "How do I put chat requests ahead of batch jobs?",

  // guides/routing/profiles-and-selectors
  "How do I choose a model that is already ready?",
  "How do I use a remote model only when my local one is busy?",
  "How do I hide a model in one profile?",

  // guides/connectivity/remote-peers, examples/peers-and-multi-host
  "How do I tell apart matching model names from different places?",
  "How do I allow more time for a slow remote service?",
  "How do I use cloud models alongside local models?",

  // guides/connectivity/upstream-passthrough
  "How do I stop website files from starting a model?",

  // guides/api-integration/api-keys-and-auth
  "How do I require API keys and change them without downtime?",
  "When do I need a reverse proxy for access control?",

  // guides/api-integration/filters-and-request-rewriting
  "How do I offer different presets without reloading a model?",
  "How do I remove settings a provider does not support?",
  "What order are model names and request settings applied in?",

  // guides/api-integration/mcp-endpoint
  "How do I connect an MCP app to llama-swap's help?",
  "Which MCP tools can check this server's setup?",

  // guides/configuration/macros
  "How do I reuse the same setup for several models?",
  "How do environment variables keep secrets out of my config?",

  // guides/operations/startup-preloading-and-hooks
  "How do I load a model when llama-swap starts?",
  "When should I run a command at startup?",

  // guides/operations/observability-storage-and-activity
  "Where can I see slow requests and model activity?",
  "How do I keep more history while I troubleshoot?",

  // guides/operations/tailcat
  "How do I limit who can reach my server through Tailcat?",
  "How do I connect to another server through Tailcat?",

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
