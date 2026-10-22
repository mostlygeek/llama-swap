import { describe, expect, it } from "vitest";
import { middleTruncate, tailStart } from "./middleTruncate";

// Every character is one unit wide, so widths are character counts.
const measure = (s: string) => s.length;

describe("tailStart", () => {
  it("moves back to a nearby separator", () => {
    const text = "andrew/qwen36-35b-a3b-q8_0";
    expect(text.slice(tailStart(text, 10))).toBe("35b-a3b-q8_0");
  });

  it("keeps the plain tail when no separator is close", () => {
    const text = "abcdefghijklmnopqrstuvwxyz";
    expect(text.slice(tailStart(text, 10))).toBe("qrstuvwxyz");
  });

  it("never takes more than half of short text", () => {
    const text = "abcdefgh";
    expect(text.slice(tailStart(text, 10))).toBe("efgh");
  });
});

describe("middleTruncate", () => {
  it("returns text that fits unchanged", () => {
    expect(middleTruncate("or/z-ai/glm-5.2", 20, measure)).toBe("or/z-ai/glm-5.2");
  });

  it("replaces the middle and keeps the tail", () => {
    const out = middleTruncate("andrew/qwen36-35b-a3b-q8_0", 20, measure);
    expect(out).toBe("andrew/…35b-a3b-q8_0");
    expect(out.length).toBe(20);
  });

  it("uses all of the available width", () => {
    const out = middleTruncate("scrappy/llama-70B-instruct-q4", 22, measure);
    expect(out).toBe("scrappy/ll…instruct-q4");
    expect(out.length).toBe(22);
  });

  it("falls back to the end of the text when the tail alone does not fit", () => {
    expect(middleTruncate("andrew/qwen36-35b-a3b-q8_0", 6, measure)).toBe("…-q8_0");
  });

  it("leaves text alone before the width is known", () => {
    expect(middleTruncate("andrew/qwen36-35b-a3b-q8_0", 0, measure)).toBe("andrew/qwen36-35b-a3b-q8_0");
  });
});
