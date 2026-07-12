export function requestHeader(
  headers: Record<string, string> | undefined,
  name: string
): string {
  if (!headers) return "";
  const wanted = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === wanted) return value;
  }
  return "";
}

export function sessionID(
  headers: Record<string, string> | undefined,
  sessionHeaders: string[]
): string {
  for (const name of sessionHeaders) {
    const value = requestHeader(headers, name);
    if (value) return value;
  }
  return "";
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unit = units[0];
  for (let i = 1; i < units.length && value >= 1024; i++) {
    value /= 1024;
    unit = units[i];
  }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${unit}`;
}

export function liveElapsedMs(
  serverElapsedMs: number,
  receivedAtMs: number | undefined,
  nowMs: number
): number {
  return serverElapsedMs + Math.max(0, nowMs - (receivedAtMs ?? nowMs));
}
