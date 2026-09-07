import { persistentStore } from "./persistent";

// Shared by Playground chat and the Docs agent. Keep the existing key so a
// user's preference from the original Playground control is preserved.
export const showGenerationStats = persistentStore<boolean>("playground-show-stats", true);
