/**
 * The saved list of Tailcat nodes this browser knows about.
 *
 * Both the connection token and the API key are bearer credentials, and both
 * are stored here in clear text. That is a deliberate trade for a page that is
 * often a file:// document with no server behind it to hold anything, and the
 * add/edit form says so plainly.
 */

import { persistentStore } from "../stores/persistent";

export interface TailcatServer {
  id: string;
  /** Free text; defaults to a short prefix of the token. */
  name: string;
  /** The "tc..." connection token from the node's log or its Tailcat page. */
  token: string;
  /** Empty unless the node configures apiKeys. */
  apiKey: string;
  /** Empty means tailcat's DefaultDERPMapURL. */
  derpMapURL: string;
}

export const tailcatServers = persistentStore<TailcatServer[]>("tailcat-servers", []);

/** The browser's own Tailcat client key, so its node key stays stable. */
export const tailcatClientKey = persistentStore<string>("tailcat-client-key", "");

export function newServerID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `srv-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

/** Tokens are case-sensitive, so this match is too. */
export function findByToken(list: TailcatServer[], token: string): TailcatServer | undefined {
  return list.find((server) => server.token === token);
}

/** A readable stand-in when the user does not name a server. */
export function defaultName(token: string): string {
  return token ? `${token.slice(0, 14)}...` : "Untitled server";
}

/** Enough of the token to recognise, not enough to use. */
export function maskToken(token: string): string {
  if (token.length <= 16) return token;
  return `${token.slice(0, 10)}...${token.slice(-4)}`;
}

/** Replaces the entry with a matching id, or appends when there is none. */
export function upsert(list: TailcatServer[], server: TailcatServer): TailcatServer[] {
  const index = list.findIndex((existing) => existing.id === server.id);
  if (index === -1) return [...list, server];
  const next = [...list];
  next[index] = server;
  return next;
}

export function remove(list: TailcatServer[], id: string): TailcatServer[] {
  return list.filter((server) => server.id !== id);
}
