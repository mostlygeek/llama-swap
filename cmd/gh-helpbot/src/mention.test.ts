import { describe, expect, it } from "vitest";

import { findMention, mentionPattern, stripQuotesAndCode } from "./mention";

describe("mentionPattern", () => {
  it("matches the login at the start or after whitespace", () => {
    expect(mentionPattern("help").test("@help what is ttl?")).toBe(true);
    expect(mentionPattern("help").test("hey @help what is ttl?")).toBe(true);
    expect(mentionPattern("help").test("hey\n@help")).toBe(true);
    expect(mentionPattern("help").test("@HELP")).toBe(true);
  });

  it("does not match longer logins or email-like text", () => {
    expect(mentionPattern("help").test("@helper")).toBe(false);
    expect(mentionPattern("help").test("@help-desk")).toBe(false);
    expect(mentionPattern("help").test("@help_me")).toBe(false);
    expect(mentionPattern("help").test("me@help")).toBe(false);
  });

  it("escapes regex characters in the mention", () => {
    expect(mentionPattern("a.b").test("@axb")).toBe(false);
    expect(mentionPattern("a.b").test("@a.b")).toBe(true);
  });
});

describe("stripQuotesAndCode", () => {
  it("removes quoted lines and fenced blocks", () => {
    const body = "> @help earlier\nreal text\n```\n@help in code\n```\n~~~\n@help tilde\n~~~\nend";
    const out = stripQuotesAndCode(body);
    expect(out).not.toContain("earlier");
    expect(out).not.toContain("in code");
    expect(out).not.toContain("tilde");
    expect(out).toContain("real text");
    expect(out).toContain("end");
  });
});

describe("findMention", () => {
  it("returns the question with the mention removed", () => {
    expect(findMention("@help how do I set ttl?")).toEqual({ found: true, query: "how do I set ttl?" });
    expect(findMention("Hey @help, how do I set ttl?")).toEqual({ found: true, query: "Hey , how do I set ttl?" });
  });

  it("keeps code blocks in the query", () => {
    const body = "@help why does this fail?\n\n```yaml\nmodels:\n  a:\n    ttl: 5\n```";
    const m = findMention(body);
    expect(m.found).toBe(true);
    expect(m.query).toContain("ttl: 5");
    expect(m.query.startsWith("why does this fail?")).toBe(true);
  });

  it("is empty when the comment is only the mention", () => {
    expect(findMention("@help")).toEqual({ found: true, query: "" });
    expect(findMention("  @help \n")).toEqual({ found: true, query: "" });
  });

  it("ignores mentions inside quotes and code", () => {
    expect(findMention("> @help what is ttl?\n\nthanks!").found).toBe(false);
    expect(findMention("```\n@help\n```").found).toBe(false);
  });

  it("handles null and other mentions", () => {
    expect(findMention(null).found).toBe(false);
    expect(findMention(undefined).found).toBe(false);
    expect(findMention("@help x", "llamahelp").found).toBe(false);
    expect(findMention("@llamahelp x", "llamahelp")).toEqual({ found: true, query: "x" });
  });
});
