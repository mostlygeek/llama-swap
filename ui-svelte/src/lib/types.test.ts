import { describe, it, expect } from "vitest";
import type { ActivityLogEntry, TokenMetrics } from "./types";

// Baseline token metrics used across tests.
const baseTokens: TokenMetrics = {
  cache_tokens: 0,
  input_tokens: 10,
  output_tokens: 5,
  prompt_per_second: 100,
  tokens_per_second: 50,
};

function makeEntry(overrides: Partial<ActivityLogEntry> = {}): ActivityLogEntry {
  return {
    id: 0,
    timestamp: "2024-01-01T00:00:00Z",
    model: "llama3",
    req_path: "/v1/chat/completions",
    resp_content_type: "application/json",
    resp_status_code: 200,
    tokens: baseTokens,
    duration_ms: 100,
    has_capture: false,
    ...overrides,
  };
}

describe("ActivityLogEntry", () => {
  describe("metadata field", () => {
    it("accepts an entry without metadata (undefined)", () => {
      const entry = makeEntry();
      expect(entry.metadata).toBeUndefined();
    });

    it("accepts an entry with metadata populated", () => {
      const entry = makeEntry({ metadata: { client: "web", trace: "abc123" } });
      expect(entry.metadata).toEqual({ client: "web", trace: "abc123" });
    });

    it("accepts an empty metadata object", () => {
      const entry = makeEntry({ metadata: {} });
      expect(entry.metadata).toEqual({});
    });

    it("allows reading a key from metadata when present", () => {
      const entry = makeEntry({ metadata: { fifo_priority: "7" } });
      expect(entry.metadata?.["fifo_priority"]).toBe("7");
    });

    it("returns undefined when accessing a missing key via optional chaining", () => {
      const entry = makeEntry();
      expect(entry.metadata?.["fifo_priority"]).toBeUndefined();
    });

    it("returns undefined for a missing key when metadata is an empty object", () => {
      const entry = makeEntry({ metadata: {} });
      expect(entry.metadata?.["missing"]).toBeUndefined();
    });

    it("supports metadata with multiple entries", () => {
      const meta: Record<string, string> = { a: "1", b: "2", c: "3" };
      const entry = makeEntry({ metadata: meta });
      expect(Object.keys(entry.metadata!)).toHaveLength(3);
      expect(entry.metadata!["b"]).toBe("2");
    });
  });

  describe("ActivityLogEntry structure", () => {
    it("round-trips through JSON with metadata", () => {
      const entry = makeEntry({ metadata: { client: "web" } });
      const json = JSON.stringify(entry);
      const parsed: ActivityLogEntry = JSON.parse(json);
      expect(parsed.metadata).toEqual({ client: "web" });
    });

    it("round-trips through JSON without metadata (field omitted)", () => {
      const entry = makeEntry();
      const json = JSON.stringify(entry);
      const parsed: ActivityLogEntry = JSON.parse(json);
      // When metadata is undefined it is dropped by JSON.stringify.
      expect(parsed.metadata).toBeUndefined();
    });

    it("round-trips through JSON with null metadata preserved as-is", () => {
      // Explicit null from a server that omits the field still satisfies
      // the optional type when accessed with optional chaining.
      const raw = { ...makeEntry(), metadata: null };
      const json = JSON.stringify(raw);
      const parsed = JSON.parse(json) as ActivityLogEntry;
      // null is falsy — optional chaining returns undefined for null too.
      expect(parsed.metadata ?? undefined).toBeUndefined();
    });
  });
});

// META_PREFIX helpers — mirror the logic in Activity.svelte so we can test it
// without importing the Svelte component.
const META_PREFIX = "meta:";

function isMetaKey(key: string): boolean {
  return key.startsWith(META_PREFIX);
}

function metaKey(name: string): string {
  return META_PREFIX + name;
}

function metaLabel(key: string): string {
  return key.slice(META_PREFIX.length);
}

describe("Activity.svelte META_PREFIX helpers", () => {
  describe("isMetaKey", () => {
    it("returns true for keys with meta: prefix", () => {
      expect(isMetaKey("meta:fifo_priority")).toBe(true);
      expect(isMetaKey("meta:client")).toBe(true);
      expect(isMetaKey("meta:")).toBe(true); // empty suffix is still prefixed
    });

    it("returns false for standard column keys", () => {
      expect(isMetaKey("id")).toBe(false);
      expect(isMetaKey("model")).toBe(false);
      expect(isMetaKey("capture")).toBe(false);
      expect(isMetaKey("duration")).toBe(false);
    });

    it("returns false for partial or incorrect prefixes", () => {
      expect(isMetaKey("meta")).toBe(false);
      expect(isMetaKey("Meta:key")).toBe(false); // case-sensitive
      expect(isMetaKey("")).toBe(false);
    });
  });

  describe("metaKey", () => {
    it("prepends META_PREFIX to the name", () => {
      expect(metaKey("fifo_priority")).toBe("meta:fifo_priority");
      expect(metaKey("client")).toBe("meta:client");
    });

    it("handles empty name", () => {
      expect(metaKey("")).toBe("meta:");
    });

    it("is the inverse of metaLabel", () => {
      const name = "some_key";
      expect(metaLabel(metaKey(name))).toBe(name);
    });
  });

  describe("metaLabel", () => {
    it("strips META_PREFIX and returns the bare name", () => {
      expect(metaLabel("meta:fifo_priority")).toBe("fifo_priority");
      expect(metaLabel("meta:client")).toBe("client");
    });

    it("handles empty suffix", () => {
      expect(metaLabel("meta:")).toBe("");
    });

    it("is the inverse of metaKey", () => {
      const key = "meta:trace_id";
      expect(metaKey(metaLabel(key))).toBe(key);
    });
  });

  describe("metadata column derivation", () => {
    it("derives unique metadata keys from a list of entries", () => {
      const entries: ActivityLogEntry[] = [
        makeEntry({ metadata: { client: "web", trace: "a" } }),
        makeEntry({ metadata: { client: "mobile" } }),
        makeEntry({ metadata: { fifo_priority: "3" } }),
        makeEntry({}), // no metadata
      ];

      const keys = Array.from(
        new Set(entries.flatMap((m) => Object.keys(m.metadata || {})))
      ).sort();

      expect(keys).toEqual(["client", "fifo_priority", "trace"]);
    });

    it("returns empty array when no entries have metadata", () => {
      const entries: ActivityLogEntry[] = [makeEntry(), makeEntry()];
      const keys = Array.from(
        new Set(entries.flatMap((m) => Object.keys(m.metadata || {})))
      ).sort();
      expect(keys).toHaveLength(0);
    });

    it("maps metadata keys to meta:-prefixed column keys", () => {
      const metaKeys = ["client", "fifo_priority"];
      const columnKeys = metaKeys.map(metaKey);
      expect(columnKeys).toEqual(["meta:client", "meta:fifo_priority"]);
    });

    it("resolves metadata value for a column key", () => {
      const entry = makeEntry({ metadata: { fifo_priority: "7", client: "web" } });
      const key = "meta:fifo_priority";
      const value = entry.metadata?.[metaLabel(key)] ?? "-";
      expect(value).toBe("7");
    });

    it("falls back to '-' for a column key not present in entry metadata", () => {
      const entry = makeEntry({ metadata: { client: "web" } });
      const key = "meta:fifo_priority";
      const value = entry.metadata?.[metaLabel(key)] ?? "-";
      expect(value).toBe("-");
    });

    it("falls back to '-' when entry has no metadata at all", () => {
      const entry = makeEntry(); // metadata is undefined
      const key = "meta:anything";
      const value = entry.metadata?.[metaLabel(key)] ?? "-";
      expect(value).toBe("-");
    });
  });
});
