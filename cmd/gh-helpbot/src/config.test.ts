import { describe, expect, it } from "vitest";

import { parseConfig, parseDuration, validate } from "./config";

const env = { GITHUB_TOKEN: "t", GITHUB_WEBHOOK_SECRET: "s", DOCS_AGENT_MODEL: "m" };

describe("parseDuration", () => {
  it("reads seconds by default and the usual units", () => {
    expect(parseDuration("60", "x")).toBe(60_000);
    expect(parseDuration("30s", "x")).toBe(30_000);
    expect(parseDuration("5m", "x")).toBe(300_000);
    expect(parseDuration("1.5h", "x")).toBe(5_400_000);
    expect(parseDuration("250ms", "x")).toBe(250);
    expect(() => parseDuration("soon", "--interval")).toThrow(/--interval/);
  });
});

describe("parseConfig", () => {
  it("defaults to serve and reads the environment", () => {
    const cfg = parseConfig([], { ...env, HELPBOT_REPOS: "a/b, c/d", HELPBOT_MENTION: "@ask", LLAMA_SWAP_URL: "http://x:1/" });
    expect(cfg.command).toBe("serve");
    expect(cfg.repos).toEqual(["a/b", "c/d"]);
    expect(cfg.mention).toBe("ask");
    expect(cfg.baseUrl).toBe("http://x:1");
    expect(cfg.model).toBe("m");
    expect(cfg.timeoutMs).toBe(600_000);
    expect(validate(cfg)).toBeUndefined();
  });

  it("lets flags win over the environment", () => {
    const cfg = parseConfig(["poll", "--repo", "x/y", "--interval", "5m", "--model", "other", "--once"], { ...env, HELPBOT_REPOS: "a/b" });
    expect(cfg.command).toBe("poll");
    expect(cfg.repos).toEqual(["x/y"]);
    expect(cfg.intervalMs).toBe(300_000);
    expect(cfg.model).toBe("other");
    expect(cfg.once).toBe(true);
    expect(validate(cfg)).toBeUndefined();
  });

  it("rejects unknown commands and bad values", () => {
    expect(() => parseConfig(["dance"], env)).toThrow(/unknown command/);
    expect(() => parseConfig(["--since", "later"], env)).toThrow(/timestamp/);
    expect(() => parseConfig(["--timeout", "-1"], env)).toThrow(/--timeout/);
    expect(() => parseConfig(["--max-iterations", "0"], env)).toThrow(/--max-iterations/);
  });
});

describe("validate", () => {
  it("names what each command is missing", () => {
    expect(validate(parseConfig([], {}))).toMatch(/--model/);
    expect(validate(parseConfig([], { DOCS_AGENT_MODEL: "m" }))).toMatch(/GITHUB_TOKEN/);
    expect(validate(parseConfig(["--dry-run"], { DOCS_AGENT_MODEL: "m" }))).toMatch(/GITHUB_WEBHOOK_SECRET/);
    expect(validate(parseConfig(["poll"], env))).toMatch(/--repo/);
    expect(validate(parseConfig(["replay"], env))).toMatch(/payload file/);
    expect(validate(parseConfig(["replay", "f.json"], env))).toMatch(/--event/);
    expect(validate(parseConfig(["ask"], env))).toMatch(/question/);
    expect(validate(parseConfig(["ask", "hi"], { DOCS_AGENT_MODEL: "m" }))).toBeUndefined();
    expect(validate(parseConfig(["action", "--dry-run"], { DOCS_AGENT_MODEL: "m" }))).toBeUndefined();
    expect(validate(parseConfig(["--listen", "nope"], env))).toMatch(/--listen/);
  });
});
