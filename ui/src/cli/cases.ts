import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { parse as parseYaml } from "yaml";

/**
 * Loading and validating evaluation cases.
 *
 * Cases are YAML so that reference answers can use block scalars; the same
 * content as JSON would be a wall of escaped newlines that nobody will
 * maintain. One file per topic, each holding a list of cases.
 *
 * Validation is strict and fails the whole load. A case with a typo'd key
 * would otherwise silently assert nothing and count as a pass, which quietly
 * inflates the score the optimization loop is steering by -- the worst
 * possible failure mode for a benchmark.
 */

export interface EvalCase {
  id: string;
  question: string;
  tags: string[];

  /** Every pattern must match the answer. */
  expectRegex: string[];
  /** At least one pattern must match the answer. */
  expectAny: string[];
  /** No pattern may match the answer. */
  forbidRegex: string[];
  /** Each named tool must have been called at least once. */
  expectTools: string[];
  /** At least one of these tools must have been called. */
  expectToolsAny: string[];
  /** No named tool may have been called. */
  forbidTools: string[];
  /** The turn must have finished within this many iterations. */
  maxIterations?: number;
  /** When true, the model must call at least one tool rather than answer from memory. */
  requireTools: boolean;

  /** Stage-2 judge material. Unused by the deterministic pass. */
  reference?: string;
  rubric: string[];

  /** Provenance, for error messages. */
  file: string;
  /** First directory below the case root. */
  group: string;
}

const KNOWN_KEYS = new Set([
  "id",
  "question",
  "tags",
  "expect_regex",
  "expect_any",
  "forbid_regex",
  "expect_tools",
  "expect_tools_any",
  "forbid_tools",
  "max_iterations",
  "require_tools",
  "reference",
  "rubric",
]);

function fail(where: string, message: string): never {
  throw new Error(`${where}: ${message}`);
}

function stringList(raw: unknown, where: string, key: string): string[] {
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw)) fail(where, `${key} must be a list`);
  return raw.map((item, i) => {
    if (typeof item !== "string") fail(where, `${key}[${i}] must be a string`);
    return item;
  });
}

function checkRegexes(patterns: string[], where: string, key: string): void {
  for (const pattern of patterns) {
    try {
      new RegExp(pattern, "i");
    } catch (error) {
      fail(
        where,
        `${key} contains an invalid regex ${JSON.stringify(pattern)}: ${String(error)}`,
      );
    }
  }
}

/** Parses one case object. Exported for tests. */
export function parseCase(
  raw: unknown,
  file: string,
  index: number,
  group = path.dirname(file).split(path.sep)[0] || "default",
): EvalCase {
  const where = `${file}[${index}]`;
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    fail(where, "each case must be a mapping");
  }
  const obj = raw as Record<string, unknown>;

  for (const key of Object.keys(obj)) {
    if (!KNOWN_KEYS.has(key)) {
      fail(
        where,
        `unknown key ${JSON.stringify(key)}; expected one of ${[...KNOWN_KEYS].sort().join(", ")}`,
      );
    }
  }

  if (typeof obj.id !== "string" || !obj.id.trim())
    fail(where, "id is required");
  if (typeof obj.question !== "string" || !obj.question.trim())
    fail(`${file}[${obj.id}]`, "question is required");

  const at = `${file}[${obj.id}]`;

  const expectRegex = stringList(obj.expect_regex, at, "expect_regex");
  const expectAny = stringList(obj.expect_any, at, "expect_any");
  const forbidRegex = stringList(obj.forbid_regex, at, "forbid_regex");
  checkRegexes(expectRegex, at, "expect_regex");
  checkRegexes(expectAny, at, "expect_any");
  checkRegexes(forbidRegex, at, "forbid_regex");

  let maxIterations: number | undefined;
  if (obj.max_iterations !== undefined) {
    if (
      typeof obj.max_iterations !== "number" ||
      !Number.isInteger(obj.max_iterations) ||
      obj.max_iterations < 1
    ) {
      fail(at, "max_iterations must be a positive integer");
    }
    maxIterations = obj.max_iterations;
  }

  if (
    obj.require_tools !== undefined &&
    typeof obj.require_tools !== "boolean"
  ) {
    fail(at, "require_tools must be a boolean");
  }
  if (obj.reference !== undefined && typeof obj.reference !== "string") {
    fail(at, "reference must be a string");
  }

  const expectTools = stringList(obj.expect_tools, at, "expect_tools");
  const expectToolsAny = stringList(
    obj.expect_tools_any,
    at,
    "expect_tools_any",
  );
  const forbidTools = stringList(obj.forbid_tools, at, "forbid_tools");
  const rubric = stringList(obj.rubric, at, "rubric");

  // A case with no assertions passes unconditionally and is worse than no case
  // at all, because it silently raises the pass rate.
  const assertions =
    expectRegex.length +
    expectAny.length +
    forbidRegex.length +
    expectTools.length +
    expectToolsAny.length +
    forbidTools.length;
  if (
    assertions === 0 &&
    maxIterations === undefined &&
    obj.require_tools === undefined
  ) {
    fail(at, "has no assertions; it would pass unconditionally");
  }

  return {
    id: obj.id,
    question: obj.question,
    tags: stringList(obj.tags, at, "tags"),
    expectRegex,
    expectAny,
    forbidRegex,
    expectTools,
    expectToolsAny,
    forbidTools,
    maxIterations,
    requireTools: obj.require_tools === true,
    reference: obj.reference,
    rubric,
    file,
    group,
  };
}

/** Parses one YAML file's worth of cases. Exported for tests. */
export function parseCaseFile(
  text: string,
  file: string,
  group?: string,
): EvalCase[] {
  const doc = parseYaml(text);
  if (doc === null || doc === undefined) return [];
  if (!Array.isArray(doc))
    fail(file, "a case file must contain a list of cases");
  return doc.map((raw, i) => parseCase(raw, file, i, group));
}

/** Loads every *.yaml under dir, sorted by relative filename then file order. */
export async function loadCases(dir: string): Promise<EvalCase[]> {
  const files: string[] = [];
  async function walk(current: string, relative = ""): Promise<void> {
    let entries;
    try {
      entries = await readdir(current, { withFileTypes: true });
    } catch {
      throw new Error(`cannot read cases directory ${dir}`);
    }
    for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
      const next = path.join(relative, entry.name);
      if (entry.isDirectory()) await walk(path.join(current, entry.name), next);
      else if (
        entry.isFile() &&
        (entry.name.endsWith(".yaml") || entry.name.endsWith(".yml"))
      )
        files.push(next);
    }
  }
  await walk(dir);
  if (files.length === 0) throw new Error(`no *.yaml case files in ${dir}`);

  const cases: EvalCase[] = [];
  for (const file of files) {
    const text = await readFile(path.join(dir, file), "utf8");
    const group = file.split(path.sep)[0] ?? "default";
    cases.push(...parseCaseFile(text, file, group));
  }

  const seen = new Map<string, string>();
  for (const c of cases) {
    const previous = seen.get(c.id);
    if (previous)
      throw new Error(
        `duplicate case id ${JSON.stringify(c.id)} in ${previous} and ${c.file}`,
      );
    seen.set(c.id, c.file);
  }

  return cases;
}
