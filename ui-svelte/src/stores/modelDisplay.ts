import { persistentStore } from "./persistent";

export const showUnlistedModels = persistentStore<boolean>(
  "models-dash-show-unlisted",
  false,
);
