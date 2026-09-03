import { describe, expect, it } from "vitest";
import { compactActivitySource } from "./activitySource";

describe("compactActivitySource", () => {
  it("shortens Tailcat node keys", () => {
    expect(
      compactActivitySource(
        "tc:nodekey:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
      )
    ).toBe("tc:0123...cdef");
  });

  it("leaves IP and short sources unchanged", () => {
    expect(compactActivitySource("ip:127.0.0.1:8080")).toBe("ip:127.0.0.1:8080");
    expect(compactActivitySource("tc:nodekey:abcd")).toBe("tc:nodekey:abcd");
    expect(compactActivitySource("")).toBe("-");
  });
});
