import { persistentStore } from "./persistent";

export const apiKey = persistentStore<string>("llama-swap-api-key", "");
