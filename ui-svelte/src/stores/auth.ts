import { persistentStore } from "./persistent";
import { get } from "svelte/store";

export const apiKey = persistentStore<string>("playground-api-key", "");

export function getAuthHeaders(): Record<string, string> {
  const key = get(apiKey);
  if (!key) return {};
  return { Authorization: `Bearer ${key}` };
}
