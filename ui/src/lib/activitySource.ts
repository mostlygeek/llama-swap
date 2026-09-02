const TAILCAT_SOURCE_PREFIX = "tc:nodekey:";

export function compactActivitySource(source: string): string {
  if (!source) return "-";
  if (!source.startsWith(TAILCAT_SOURCE_PREFIX)) return source;

  const nodeID = source.slice(TAILCAT_SOURCE_PREFIX.length);
  if (nodeID.length <= 8) return source;
  return `tc:${nodeID.slice(0, 4)}...${nodeID.slice(-4)}`;
}
