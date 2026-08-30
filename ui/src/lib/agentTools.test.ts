import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CODE_UNSUPPORTED_PROTOCOL_VERSION,
  MCP_ENDPOINT,
  MCP_PROTOCOL_VERSION,
  McpError,
  callTool,
  fetchToolDefinitions,
  friendlyToolName,
  isUsableToolName,
} from "./agentTools";

type FetchCall = { url: string; init: RequestInit };

/** Stubs global fetch with a canned JSON-RPC response and records the calls. */
function stubFetch(response: unknown, status = 200): FetchCall[] {
  const calls: FetchCall[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      return {
        ok: status >= 200 && status < 300,
        status,
        json: async () => response,
        text: async () => JSON.stringify(response),
      } as Response;
    })
  );
  return calls;
}

function bodyOf(call: FetchCall): any {
  return JSON.parse(call.init.body as string);
}

function headersOf(call: FetchCall): Record<string, string> {
  return call.init.headers as Record<string, string>;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("transport", () => {
  it("posts JSON-RPC to /api/mcp with the standard MCP headers", async () => {
    const calls = stubFetch({ jsonrpc: "2.0", id: 1, result: { tools: [] } });
    await fetchToolDefinitions();

    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe(MCP_ENDPOINT);
    expect(calls[0].init.method).toBe("POST");

    const headers = headersOf(calls[0]);
    expect(headers["Mcp-Protocol-Version"]).toBe(MCP_PROTOCOL_VERSION);
    expect(headers["Mcp-Method"]).toBe("tools/list");
    expect(headers["Content-Type"]).toBe("application/json");
    // tools/list carries no name, so the header must be absent rather than empty.
    expect(headers).not.toHaveProperty("Mcp-Name");

    const body = bodyOf(calls[0]);
    expect(body.jsonrpc).toBe("2.0");
    expect(body.method).toBe("tools/list");
    expect(typeof body.id).toBe("number");
  });

  // The server rejects a Mcp-Name that disagrees with params.name.
  it("sets Mcp-Name on tools/call to match params.name", async () => {
    const calls = stubFetch({ jsonrpc: "2.0", id: 1, result: { content: [] } });
    await callTool("get_doc", { id: "guides/configuration/macros" });

    const headers = headersOf(calls[0]);
    expect(headers["Mcp-Method"]).toBe("tools/call");
    expect(headers["Mcp-Name"]).toBe("get_doc");

    const body = bodyOf(calls[0]);
    expect(body.method).toBe("tools/call");
    expect(body.params).toEqual({ name: "get_doc", arguments: { id: "guides/configuration/macros" } });
  });

  it("sends an empty arguments object when there are none", async () => {
    const calls = stubFetch({ jsonrpc: "2.0", id: 1, result: { content: [] } });
    await callTool("list_docs", undefined);

    expect(bodyOf(calls[0]).params.arguments).toEqual({});
  });

  it("uses a fresh request id per call", async () => {
    const calls = stubFetch({ jsonrpc: "2.0", id: 1, result: { tools: [] } });
    await fetchToolDefinitions();
    await fetchToolDefinitions();

    expect(bodyOf(calls[0]).id).not.toBe(bodyOf(calls[1]).id);
  });

  it("turns a JSON-RPC error into a thrown McpError", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, error: { code: -32601, message: "unknown method" } });

    await expect(callTool("nope", {})).rejects.toMatchObject({
      name: "McpError",
      code: -32601,
      message: "unknown method",
    });
  });

  it("throws on a transport failure", async () => {
    stubFetch({ error: "boom" }, 500);
    await expect(callTool("list_docs", {})).rejects.toThrow(/MCP transport error: 500/);
  });
});

describe("fetchToolDefinitions", () => {
  const mcpTools = {
    tools: [
      {
        name: "get_doc",
        title: "Read a llama-swap document",
        description: "Read one document by id.",
        inputSchema: { type: "object", properties: { id: { type: "string" } }, required: ["id"] },
        annotations: { readOnlyHint: true },
      },
    ],
  };

  it("maps MCP tools to OpenAI-shaped definitions", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, result: mcpTools });

    expect(await fetchToolDefinitions()).toEqual([
      {
        type: "function",
        function: {
          name: "get_doc",
          description: "Read one document by id.",
          // inputSchema becomes the OpenAI `parameters` schema unchanged.
          parameters: { type: "object", properties: { id: { type: "string" } }, required: ["id"] },
          // The MCP `title` is kept as UI-only display metadata.
          title: "Read a llama-swap document",
        },
      },
    ]);
  });

  it("falls back to the title when a tool has no description", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, result: { tools: [{ name: "x", title: "The X tool", inputSchema: {} }] } });

    const [tool] = await fetchToolDefinitions();
    expect(tool.function.description).toBe("The X tool");
  });

  it("supplies an empty schema when a tool omits one", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, result: { tools: [{ name: "x" }] } });

    const [tool] = await fetchToolDefinitions();
    expect(tool.function.parameters).toEqual({ type: "object", properties: {} });
  });

  it("drops entries with no name", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, result: { tools: [{ description: "nameless" }, { name: "ok" }] } });

    const tools = await fetchToolDefinitions();
    expect(tools.map((t) => t.function.name)).toEqual(["ok"]);
  });

  // A name MCP allows but an OpenAI-shaped backend rejects is dropped here
  // rather than sent and turned into an error the user cannot act on.
  it("drops names that cannot be used as OpenAI function names", async () => {
    stubFetch({
      jsonrpc: "2.0",
      id: 1,
      result: {
        tools: [
          { name: "docs__get_doc" },
          { name: "upstream.search" },              // a dot: legal in MCP, not here
          { name: `long__${"x".repeat(60)}` },      // 66 characters
          { name: "sys__now" },
        ],
      },
    });

    const tools = await fetchToolDefinitions();
    expect(tools.map((t) => t.function.name)).toEqual(["docs__get_doc", "sys__now"]);
  });

  // Agent mode should simply stay off rather than erroring at the user.
  it("returns [] when the server has nothing indexed", async () => {
    stubFetch({ enabled: false }, 503);
    expect(await fetchToolDefinitions()).toEqual([]);
  });

  it("returns [] when the server speaks a different MCP revision", async () => {
    stubFetch({
      jsonrpc: "2.0",
      id: 1,
      error: {
        code: CODE_UNSUPPORTED_PROTOCOL_VERSION,
        message: "unsupported protocol version",
        data: { supported: ["2027-01-01"], requested: MCP_PROTOCOL_VERSION },
      },
    });

    expect(await fetchToolDefinitions()).toEqual([]);
  });

  it("propagates other JSON-RPC errors", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, error: { code: -32603, message: "internal" } });
    await expect(fetchToolDefinitions()).rejects.toBeInstanceOf(McpError);
  });

  it("returns [] when the result has no tools array", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, result: {} });
    expect(await fetchToolDefinitions()).toEqual([]);
  });
});

describe("callTool", () => {
  it("joins the text blocks of the result", async () => {
    stubFetch({
      jsonrpc: "2.0",
      id: 1,
      result: {
        content: [
          { type: "text", text: "first" },
          { type: "text", text: "second" },
        ],
      },
    });

    expect(await callTool("list_docs", {})).toEqual({ text: "first\nsecond", isError: false });
  });

  it("ignores non-text content blocks", async () => {
    stubFetch({
      jsonrpc: "2.0",
      id: 1,
      result: {
        content: [
          { type: "image", data: "..." },
          { type: "text", text: "kept" },
        ],
      },
    });

    expect((await callTool("list_docs", {})).text).toBe("kept");
  });

  // A tool that ran but could not answer is a normal result carrying its own
  // correction, not a transport failure.
  it("reports isError without throwing", async () => {
    stubFetch({
      jsonrpc: "2.0",
      id: 1,
      result: { content: [{ type: "text", text: 'Error: no document with id "x"' }], isError: true },
    });

    expect(await callTool("get_doc", { id: "x" })).toEqual({
      text: 'Error: no document with id "x"',
      isError: true,
    });
  });

  it("handles a result with no content", async () => {
    stubFetch({ jsonrpc: "2.0", id: 1, result: {} });
    expect(await callTool("list_docs", {})).toEqual({ text: "", isError: false });
  });
});

describe("isUsableToolName", () => {
  it.each([
    ["docs__get_doc", true],
    ["sys__now", true],
    ["with-hyphen", true],
    ["Mixed_Case9", true],
    ["upstream.search", false],   // MCP allows "." ; OpenAI function names do not
    ["has space", false],
    ["", false],
    ["x".repeat(64), true],
    ["x".repeat(65), false],      // 64 is the OpenAI limit, not MCP's 128
  ])("%s -> %s", (name, want) => {
    expect(isUsableToolName(name)).toBe(want);
  });
});

describe("friendlyToolName", () => {
  it("prefers the MCP title when present", () => {
    expect(friendlyToolName("docs__search_docs", "Search llama-swap documentation")).toBe(
      "Search llama-swap documentation"
    );
  });

  it("humanizes the local name when there is no title", () => {
    expect(friendlyToolName("docs__search_docs")).toBe("Search Docs");
    expect(friendlyToolName("sys__now")).toBe("Now");
    expect(friendlyToolName("get_config_schema")).toBe("Get Config Schema");
  });

  it("ignores a blank title", () => {
    expect(friendlyToolName("docs__get_doc", "   ")).toBe("Get Doc");
  });
});
