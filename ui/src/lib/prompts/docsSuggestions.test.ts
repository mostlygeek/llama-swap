import { describe, expect, it } from "vitest";
import { DOCS_SUGGESTIONS, SUGGESTION_COUNT, pickSuggestions } from "./docsSuggestions";

describe("pickSuggestions", () => {
  it("draws the number the Help page shows", () => {
    expect(pickSuggestions()).toHaveLength(SUGGESTION_COUNT);
  });

  it("never repeats a question in one draw", () => {
    for (let attempt = 0; attempt < 50; attempt++) {
      const drawn = pickSuggestions();
      expect(new Set(drawn).size).toBe(drawn.length);
    }
  });

  it("only draws questions from the pool", () => {
    for (const suggestion of pickSuggestions(SUGGESTION_COUNT * 2)) {
      expect(DOCS_SUGGESTIONS).toContain(suggestion);
    }
  });

  it("returns the whole pool rather than padding when asked for more than it has", () => {
    expect(pickSuggestions(DOCS_SUGGESTIONS.length + 10)).toHaveLength(DOCS_SUGGESTIONS.length);
  });

  // A question that can never be drawn is a question nobody wrote. This catches
  // an off-by-one in the shuffle, which is the classic way to strand an entry.
  it("can eventually draw every question in the pool", () => {
    const seen = new Set<string>();
    for (let attempt = 0; attempt < 2000 && seen.size < DOCS_SUGGESTIONS.length; attempt++) {
      for (const suggestion of pickSuggestions()) seen.add(suggestion);
    }
    expect(seen.size).toBe(DOCS_SUGGESTIONS.length);
  });

  it("offers enough questions that a repeat visit is a different set", () => {
    expect(DOCS_SUGGESTIONS.length).toBeGreaterThanOrEqual(SUGGESTION_COUNT * 4);
  });

  it("asks the question that sends the reader looking for what they are missing", () => {
    expect(DOCS_SUGGESTIONS).toContain("What llama-swap features am I not using?");
  });
});
