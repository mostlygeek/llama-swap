// In-flight request helpers. Ported from lib/inflight.ts.

export function requestHeader(headers, name) {
  if (!headers) return "";
  const wanted = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === wanted) return value;
  }
  return "";
}

export function sessionID(headers, sessionHeaders) {
  for (const name of sessionHeaders) {
    const value = requestHeader(headers, name);
    if (value) return value;
  }
  return "";
}

export function formatBytes(bytes) {
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

export function liveElapsedMs(serverElapsedMs, receivedAtMs, nowMs) {
  return serverElapsedMs + Math.max(0, nowMs - (receivedAtMs ?? nowMs));
}
