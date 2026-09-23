import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { HANDLED_CAP, loadState, markHandled, saveState } from "./state";

let dir: string;
beforeEach(async () => {
  dir = await mkdtemp(path.join(os.tmpdir(), "helpbot-state-"));
});
afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

describe("state file", () => {
  it("is undefined when missing", async () => {
    expect(await loadState(path.join(dir, "missing.json"))).toBeUndefined();
  });

  it("round-trips and creates parent directories", async () => {
    const file = path.join(dir, "nested", "state.json");
    await saveState(file, { since: "2026-09-18T00:00:00.000Z", handled: ["a", "b"] });
    expect(await loadState(file)).toEqual({ since: "2026-09-18T00:00:00.000Z", handled: ["a", "b"] });
    expect(JSON.parse(await readFile(file, "utf8"))).toHaveProperty("since");
  });

  it("rejects a file without a timestamp", async () => {
    const file = path.join(dir, "bad.json");
    await saveState(file, { since: "2026-09-18T00:00:00.000Z", handled: [] });
    const { writeFile } = await import("node:fs/promises");
    await writeFile(file, JSON.stringify({ since: "yesterday" }));
    await expect(loadState(file)).rejects.toThrow(/timestamp/);
  });

  it("caps the handled list", async () => {
    const handled = Array.from({ length: HANDLED_CAP + 10 }, (_, i) => `id${i}`);
    const file = path.join(dir, "cap.json");
    await saveState(file, { since: "2026-09-18T00:00:00.000Z", handled });
    const loaded = await loadState(file);
    expect(loaded?.handled).toHaveLength(HANDLED_CAP);
    expect(loaded?.handled[0]).toBe("id10");
  });
});

describe("markHandled", () => {
  it("appends once", () => {
    const s = { since: "t", handled: ["a"] };
    expect(markHandled(s, "b").handled).toEqual(["a", "b"]);
    expect(markHandled(s, "a")).toBe(s);
  });
});
