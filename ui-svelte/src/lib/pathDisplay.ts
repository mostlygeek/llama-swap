export type HomeAliasStyle = "tilde" | "env";

export function collapseHomePath(path: string, style: HomeAliasStyle = "tilde"): string {
  const raw = (path || "").trim();
  if (!raw) return raw;

  const normalized = raw.replace(/\\/g, "/");
  const homeToken = style === "env" ? "$HOME" : "~";
  const homePrefixes = [/^\/home\/[^/]+/i, /^\/Users\/[^/]+/];

  for (const prefix of homePrefixes) {
    const match = normalized.match(prefix);
    if (!match) continue;
    const suffix = normalized.slice(match[0].length);
    return `${homeToken}${suffix}`;
  }

  return raw;
}
