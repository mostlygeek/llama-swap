import { describe, it, expect } from "vitest";
import { defaultName, findByToken, maskToken, remove, upsert, type TailcatServer } from "./servers";

function server(id: string, token: string): TailcatServer {
  return { id, name: id, token, apiKey: "", derpMapURL: "" };
}

describe("findByToken", () => {
  const list = [server("a", "tcAAA"), server("b", "tcBBB")];

  it("finds the saved entry so its API key comes along with a #token link", () => {
    expect(findByToken(list, "tcBBB")?.id).toBe("b");
  });

  it("returns nothing for an unknown token", () => {
    expect(findByToken(list, "tcCCC")).toBeUndefined();
  });

  // Tokens are case-sensitive; the Tailcat docs say so explicitly, and matching
  // loosely here would silently attach the wrong API key.
  it("does not match on case", () => {
    expect(findByToken(list, "tcaaa")).toBeUndefined();
  });
});

describe("upsert", () => {
  it("appends an entry with a new id", () => {
    const list = upsert([server("a", "tcAAA")], server("b", "tcBBB"));
    expect(list.map((s) => s.id)).toEqual(["a", "b"]);
  });

  it("replaces in place, so editing does not duplicate the entry", () => {
    const edited = { ...server("a", "tcNEW"), name: "renamed" };
    const list = upsert([server("a", "tcAAA"), server("b", "tcBBB")], edited);
    expect(list).toHaveLength(2);
    expect(list[0]).toEqual(edited);
    expect(list[1].id).toBe("b");
  });

  it("leaves the original array alone", () => {
    const original = [server("a", "tcAAA")];
    upsert(original, server("b", "tcBBB"));
    expect(original).toHaveLength(1);
  });
});

describe("remove", () => {
  it("drops only the named entry", () => {
    expect(remove([server("a", "tcAAA"), server("b", "tcBBB")], "a").map((s) => s.id)).toEqual(["b"]);
  });
});

describe("maskToken", () => {
  it("shows enough of a long token to recognise it", () => {
    const token = "tc" + "x".repeat(140);
    const masked = maskToken(token);
    expect(masked.startsWith("tcxxxxxxxx")).toBe(true);
    expect(masked).toContain("...");
    expect(masked.length).toBeLessThan(token.length);
  });

  it("leaves a short value alone rather than mangling it", () => {
    expect(maskToken("tcshort")).toBe("tcshort");
  });
});

describe("defaultName", () => {
  it("names an unnamed server after its token", () => {
    expect(defaultName("tcABCDEFGHIJKLMNOP")).toBe("tcABCDEFGHIJKL...");
  });

  it("has something to show before a token is typed", () => {
    expect(defaultName("")).toBe("Untitled server");
  });
});
