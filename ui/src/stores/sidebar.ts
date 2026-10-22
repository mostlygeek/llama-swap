import { persistentStore } from "./persistent";
import { SIDEBAR_WIDTH_DEFAULT_PX } from "$lib/components/ui/sidebar/constants.js";

export const modelsMenuOpen = persistentStore<boolean>("models-menu-open", true);

// Desktop sidebar expanded/collapsed state and its user-resized width (px).
export const sidebarOpen = persistentStore<boolean>("sidebar-open", true);
export const sidebarWidth = persistentStore<number>("sidebar-width", SIDEBAR_WIDTH_DEFAULT_PX);
