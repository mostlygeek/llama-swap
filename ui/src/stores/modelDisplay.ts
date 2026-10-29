import { persistentStore } from "./persistent";

export const showUnlistedModels = persistentStore<boolean>(
  "models-dash-show-unlisted",
  false,
);

// Models list view: group local models into capability-derived class
// sections (true, the default) or show one unified list (false). Persisted so
// the dashboard opens the way it was last left.
export const groupByClass = persistentStore<boolean>(
  "models-dash-group-by-class",
  true,
);

// Sections the user collapsed on the Models list, keyed by stable section id
// (class name, "local", "peers"). Missing/false means expanded; sections
// default open. Persisted so a collapsed Chat section stays collapsed across
// route changes and reloads.
export const collapsedSections = persistentStore<Record<string, boolean>>(
  "models-dash-collapsed-sections",
  {},
);

// Show a curated set of capability badges (context window, vision, tools) on
// each Models list row. Off by default to keep the list dense.
export const showCapabilityTags = persistentStore<boolean>(
  "models-dash-show-capability-tags",
  false,
);
