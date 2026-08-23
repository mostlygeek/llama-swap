import { describe, it, expect } from "vitest";
import { isComposingKey, isSubmitEnter } from "./ime";

// Shorthand for the subset of KeyboardEvent the helpers read.
function key(
  overrides: Partial<Pick<KeyboardEvent, "key" | "shiftKey" | "isComposing" | "keyCode">> = {},
) {
  return { key: "Enter", shiftKey: false, isComposing: false, keyCode: 13, ...overrides };
}

describe("isComposingKey", () => {
  it("is true while a composition session is active", () => {
    expect(isComposingKey(key({ isComposing: true }))).toBe(true);
  });

  it("is true for the legacy composing keyCode", () => {
    expect(isComposingKey(key({ keyCode: 229 }))).toBe(true);
  });

  it("is false for a normal keypress", () => {
    expect(isComposingKey(key())).toBe(false);
  });
});

describe("isSubmitEnter", () => {
  it("submits on a plain Enter", () => {
    expect(isSubmitEnter(key())).toBe(true);
  });

  it("does not submit on the Enter that confirms an IME conversion", () => {
    expect(isSubmitEnter(key({ isComposing: true }))).toBe(false);
    expect(isSubmitEnter(key({ isComposing: true, keyCode: 229 }))).toBe(false);
  });

  it("does not submit when only the legacy composing keyCode is set", () => {
    expect(isSubmitEnter(key({ keyCode: 229 }))).toBe(false);
  });

  it("does not submit on Shift+Enter, which inserts a newline", () => {
    expect(isSubmitEnter(key({ shiftKey: true }))).toBe(false);
  });

  it("ignores other keys", () => {
    expect(isSubmitEnter(key({ key: "a", keyCode: 65 }))).toBe(false);
    expect(isSubmitEnter(key({ key: "Escape", keyCode: 27 }))).toBe(false);
  });
});
