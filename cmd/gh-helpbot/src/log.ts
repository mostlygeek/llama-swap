/** One line per event on stderr, so the container's log is the audit trail. */
export function log(message: string, fields: Record<string, unknown> = {}): void {
  const extra = Object.entries(fields)
    .filter(([, v]) => v !== undefined && v !== "")
    .map(([k, v]) => `${k}=${typeof v === "string" && /\s/.test(v) ? JSON.stringify(v) : String(v)}`)
    .join(" ");
  process.stderr.write(`${new Date().toISOString()} ${message}${extra ? " " + extra : ""}\n`);
}
