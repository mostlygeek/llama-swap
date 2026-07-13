import { get } from "svelte/store";
import { apiKey } from "../stores/auth";

export function getAuthHeaders(existingHeaders: Record<string, string> = {}): Record<string, string> {
  const headers = { ...existingHeaders };
  const key = get(apiKey);
  if (key) {
    headers["Authorization"] = `Bearer ${key}`;
  }
  return headers;
}
