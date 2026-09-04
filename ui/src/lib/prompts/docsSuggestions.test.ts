import { describe, expect, it } from "vitest";
import {
  DOCS_SUGGESTIONS,
  NUMBERED_SUGGESTIONS,
  SUGGESTION_COUNT,
  pickSuggestions,
} from "./docsSuggestions";

describe("pickSuggestions", () => {
  it("draws the number the Help page shows", () => {
    expect(pickSuggestions()).toHaveLength(SUGGESTION_COUNT);
  });

  it("never repeats a question in one draw", () => {
    for (let attempt = 0; attempt < 50; attempt++) {
      const drawn = pickSuggestions();
      expect(new Set(drawn.map((s) => s.number)).size).toBe(drawn.length);
    }
  });

  it("only draws questions from the pool", () => {
    for (const suggestion of pickSuggestions(SUGGESTION_COUNT * 2)) {
      expect(NUMBERED_SUGGESTIONS).toContainEqual(suggestion);
    }
  });

  // Four numbers going up read as a list; the same four shuffled read as a
  // mistake. The draw is random, the order is not.
  it("shows the drawn questions in ascending order", () => {
    for (let attempt = 0; attempt < 50; attempt++) {
      const numbers = pickSuggestions().map((s) => s.number);
      expect(numbers).toEqual([...numbers].sort((a, b) => a - b));
    }
  });

  it("returns the whole pool rather than padding when asked for more than it has", () => {
    expect(pickSuggestions(DOCS_SUGGESTIONS.length + 10)).toHaveLength(DOCS_SUGGESTIONS.length);
  });

  // A question that can never be drawn is a question nobody wrote. This catches
  // an off-by-one in the shuffle, which is the classic way to strand an entry.
  it("can eventually draw every question in the pool", () => {
    const seen = new Set<number>();
    for (let attempt = 0; attempt < 2000 && seen.size < DOCS_SUGGESTIONS.length; attempt++) {
      for (const suggestion of pickSuggestions()) seen.add(suggestion.number);
    }
    expect(seen.size).toBe(DOCS_SUGGESTIONS.length);
  });

  it("offers enough questions that a repeat visit is a different set", () => {
    expect(DOCS_SUGGESTIONS.length).toBeGreaterThanOrEqual(SUGGESTION_COUNT * 4);
  });

  it("asks the question that sends the reader looking for what they are missing", () => {
    expect(DOCS_SUGGESTIONS).toContain("What can llama-swap do that I'm not using?");
  });
});

describe("NUMBERED_SUGGESTIONS", () => {
  // The number names a topic, so the same one has to mean the same question
  // every time it is shown.
  it("numbers every question by its place in the pool, from 1", () => {
    expect(NUMBERED_SUGGESTIONS).toHaveLength(DOCS_SUGGESTIONS.length);
    NUMBERED_SUGGESTIONS.forEach((suggestion, index) => {
      expect(suggestion.number).toBe(index + 1);
      expect(suggestion.question).toBe(DOCS_SUGGESTIONS[index]);
    });
  });

  it("gives no two questions the same number", () => {
    expect(new Set(NUMBERED_SUGGESTIONS.map((s) => s.number)).size).toBe(NUMBERED_SUGGESTIONS.length);
  });

  it("lists no question twice", () => {
    expect(new Set(DOCS_SUGGESTIONS).size).toBe(DOCS_SUGGESTIONS.length);
  });
});
