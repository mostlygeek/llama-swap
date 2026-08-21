import { persistentStore } from "./persistent";

export const modelsMenuOpen = persistentStore<boolean>("models-menu-open", true);
