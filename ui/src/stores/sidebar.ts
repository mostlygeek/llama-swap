import { persistentStore } from "./persistent";
import { SIDEBAR_WIDTH_DEFAULT_PX } from "$lib/components/ui/sidebar/constants.js";

export const modelsMenuOpen = persistentStore<boolean>("models-menu-open", true);

// Category groups inside the Models sub-menu that are folded, keyed by class
// id. Missing means open, so groups default to expanded and deleting the key
// re-opens. Same missing-means-open semantics as the dashboard's
// collapsedSections, so a folded group stays folded across reloads.
export const modelsGroupsCollapsed = persistentStore<Record<string, boolean>>(
  "models-groups-collapsed",
  {},
);

// Desktop sidebar expanded/collapsed state and its user-resized width (px).
export const sidebarOpen = persistentStore<boolean>("sidebar-open", true);
export const sidebarWidth = persistentStore<number>("sidebar-width", SIDEBAR_WIDTH_DEFAULT_PX);
