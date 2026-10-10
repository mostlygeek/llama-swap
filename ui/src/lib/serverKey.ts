import { get } from "svelte/store";
import { activeServerID, tailcatServers, findByID } from "../tailcat/servers";

/**
 * Identifies the backend the playground is currently talking to, so per-server
 * state (like the last-selected model) does not leak between servers. The
 * Tailcat page can reach many different nodes from one browser under the same
 * origin, so window.location.host alone can't tell them apart; the ordinary
 * llama-swap UI only ever has one backend, so its hostname is distinctive
 * enough on its own.
 */
export function currentServerKey(): string {
  const server = findByID(get(tailcatServers), get(activeServerID));
  if (server) return `tailcat:${server.id}`;
  return typeof window !== "undefined" ? window.location.host : "default";
}

/** Namespaces a persistent-store key by the currently active server. */
export function perServerKey(key: string): string {
  return `${key}::${currentServerKey()}`;
}
