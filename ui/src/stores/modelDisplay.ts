import { persistentStore } from "./persistent";

export const showUnlistedModels = persistentStore<boolean>(
  "models-dash-show-unlisted",
  false,
);

// Show a curated set of capability badges (context window, vision, tools) on
// each Models list row. Off by default to keep the list dense.
export const showCapabilityTags = persistentStore<boolean>(
  "models-dash-show-capability-tags",
  false,
);
