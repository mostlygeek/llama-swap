import { describe, expect, it } from "vitest";

import { formatReply, hasMarker, marker, sanitizeAnswer, MAX_ANSWER_CHARS } from "./reply";

describe("marker", () => {
  it("round-trips through hasMarker", () => {
    const body = `answer\n${marker("IC_1")}\n`;
    expect(hasMarker(body, "IC_1")).toBe(true);
    expect(hasMarker(body, "IC_10")).toBe(false);
    expect(hasMarker(null, "IC_1")).toBe(false);
  });
});

describe("sanitizeAnswer", () => {
  it("strips HTML comments so a marker cannot be spoofed", () => {
    expect(sanitizeAnswer(`text ${marker("X")} more <!-- hi\nthere -->`)).toBe("text  more");
  });

  it("neutralises @mentions outside code", () => {
    expect(sanitizeAnswer("ask @mostlygeek or @some-one_2")).toBe("ask `@mostlygeek` or `@some-one_2`");
    expect(sanitizeAnswer("already `@quoted`")).toBe("already `@quoted`");
    expect(sanitizeAnswer("me@example.com")).toBe("me@example.com");
  });
});

describe("formatReply", () => {
  const meta = { id: "IC_1", model: "gemma", mention: "help" };

  it("appends the footer and the marker", () => {
    const body = formatReply("Set `ttl: 300`.", meta);
    expect(body.startsWith("Set `ttl: 300`.\n\n---\n<sub>")).toBe(true);
    expect(body).toContain("`gemma`");
    expect(body).toContain("`@help`");
    expect(body).toContain("https://github.com/mostlygeek/llama-swap/tree/main/docs");
    expect(body.trimEnd().endsWith(marker("IC_1"))).toBe(true);
  });

  it("uses a custom docs url", () => {
    expect(formatReply("x", { ...meta, docsUrl: "https://example.test/docs" })).toContain("https://example.test/docs");
  });

  it("truncates very long answers under GitHub's limit", () => {
    const body = formatReply("x".repeat(MAX_ANSWER_CHARS + 500), meta);
    expect(body).toContain("[answer truncated]");
    expect(body.length).toBeLessThan(65536);
    expect(hasMarker(body, "IC_1")).toBe(true);
  });
});
