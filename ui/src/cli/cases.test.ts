import { describe, it, expect } from "vitest";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { loadCases, parseCase, parseCaseFile } from "./cases";

const minimal = { id: "x", question: "q?", expect_regex: ["a"] };

describe("parseCase", () => {
  it("parses a full case", () => {
    const c = parseCase(
      {
        id: "ttl-1",
        question: "how?",
        tags: ["ttl"],
        expect_regex: ["ttl"],
        expect_any: ["second"],
        forbid_regex: ["nope"],
        expect_tools: ["docs__search_docs"],
        forbid_tools: ["sys__now"],
        max_iterations: 4,
        require_tools: true,
        reference: "ref",
        rubric: ["says seconds"],
      },
      "f.yaml",
      0,
    );
    expect(c.id).toBe("ttl-1");
    expect(c.expectTools).toEqual(["docs__search_docs"]);
    expect(c.maxIterations).toBe(4);
    expect(c.requireTools).toBe(true);
    expect(c.rubric).toEqual(["says seconds"]);
  });

  it("defaults optional fields", () => {
    const c = parseCase(minimal, "f.yaml", 0);
    expect(c.tags).toEqual([]);
    expect(c.forbidRegex).toEqual([]);
    expect(c.requireTools).toBe(false);
    expect(c.maxIterations).toBeUndefined();
  });

  // A typo'd key would otherwise assert nothing and silently count as a pass,
  // inflating the very number the optimization loop steers by.
  it("rejects an unknown key", () => {
    expect(() =>
      parseCase({ ...minimal, expct_regex: ["a"] }, "f.yaml", 0),
    ).toThrow(/unknown key/);
  });

  it("rejects a case with no assertions", () => {
    expect(() => parseCase({ id: "x", question: "q?" }, "f.yaml", 0)).toThrow(
      /no assertions/,
    );
  });

  it("rejects an invalid regex rather than failing at run time", () => {
    expect(() =>
      parseCase({ ...minimal, expect_regex: ["("] }, "f.yaml", 0),
    ).toThrow(/invalid regex/);
  });

  it("requires id and question", () => {
    expect(() => parseCase({ question: "q?" }, "f.yaml", 0)).toThrow(
      /id is required/,
    );
    expect(() => parseCase({ id: "x" }, "f.yaml", 0)).toThrow(
      /question is required/,
    );
  });

  it("rejects a non-integer max_iterations", () => {
    expect(() =>
      parseCase({ ...minimal, max_iterations: 0 }, "f.yaml", 0),
    ).toThrow(/positive integer/);
  });

  it("rejects a non-list where a list is expected", () => {
    expect(() => parseCase({ ...minimal, tags: "ttl" }, "f.yaml", 0)).toThrow(
      /tags must be a list/,
    );
  });
});

describe("loadCases", () => {
  it("recursively loads sorted groups and rejects duplicate IDs across them", async () => {
    const dir = await mkdtemp(path.join(tmpdir(), "llama-swap-cases-"));
    try {
      await mkdir(path.join(dir, "routing"));
      await mkdir(path.join(dir, "operations"));
      await writeFile(
        path.join(dir, "routing", "a.yaml"),
        "- id: route\n  question: q\n  expect_regex: [q]\n",
      );
      await writeFile(
        path.join(dir, "operations", "b.yaml"),
        "- id: ops\n  question: q\n  expect_regex: [q]\n",
      );
      const cases = await loadCases(dir);
      expect(cases.map((c) => c.id)).toEqual(["ops", "route"]);
      expect(cases.map((c) => c.group)).toEqual(["operations", "routing"]);

      await writeFile(
        path.join(dir, "routing", "duplicate.yaml"),
        "- id: ops\n  question: q\n  expect_regex: [q]\n",
      );
      await expect(loadCases(dir)).rejects.toThrow(
        /duplicate case id.*operations\/b.yaml.*routing\/duplicate.yaml/,
      );
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });
});

describe("parseCaseFile", () => {
  it("parses a list of cases", () => {
    const cases = parseCaseFile(
      `- id: a\n  question: q?\n  expect_regex: ["x"]\n`,
      "f.yaml",
    );
    expect(cases).toHaveLength(1);
    expect(cases[0].file).toBe("f.yaml");
  });

  it("treats an empty file as no cases", () => {
    expect(parseCaseFile("", "f.yaml")).toEqual([]);
  });

  it("rejects a mapping at the top level", () => {
    expect(() => parseCaseFile(`id: a\n`, "f.yaml")).toThrow(
      /must contain a list/,
    );
  });
});
