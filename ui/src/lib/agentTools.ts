import { playgroundSessionHeaders } from "./playgroundSession";
import type { ToolDefinition } from "./chatApi";

/**
 * A minimal MCP client for llama-swap's own /api/mcp endpoint.
 *
 * The server speaks MCP 2026-07-28, the revision that dropped the
 * initialize/initialized handshake and the session header, so a client is just
 * a POST with three headers. There is no connection to set up and nothing to
 * tear down.
 *
 * Kept separate from agentLoop.ts so the loop itself stays free of fetch and
 * can be unit tested without a network.
 */

export const MCP_ENDPOINT = "/api/mcp";
export const MCP_PROTOCOL_VERSION = "2026-07-28";

/** JSON-RPC error code for a protocol version the server does not speak. */
export const CODE_UNSUPPORTED_PROTOCOL_VERSION = -32022;

/** The server is running but has no documentation indexed. */
export class McpUnavailableError extends Error {
  constructor() {
    super("The MCP endpoint has no documentation indexed");
    this.name = "McpUnavailableError";
  }
}

/** A JSON-RPC error returned by the server. */
export class McpError extends Error {
  code: number;
  constructor(code: number, message: string) {
    super(message);
    this.name = "McpError";
    this.code = code;
  }
}

export interface ToolCallOutcome {
  text: string;
  /** True when the tool ran but could not answer, so the model can retry. */
  isError: boolean;
}

interface McpTool {
  name: string;
  title?: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
}

/**
 * MCP permits tool names matching [a-zA-Z0-9_-.] up to 128 characters, but these
 * names are forwarded as OpenAI function names, whose convention is
 * [a-zA-Z0-9_-] up to 64. A name outside that intersection is rejected by the
 * backend, so it is dropped here rather than sent and turned into an error the
 * user cannot act on.
 *
 * llama-swap's own registry already enforces the tighter rule, so this only
 * fires for a proxied upstream that does not.
 */
const MAX_FUNCTION_NAME_LEN = 64;
const FUNCTION_NAME_PATTERN = /^[a-zA-Z0-9_-]+$/;

export function isUsableToolName(name: string): boolean {
  return (
    typeof name === "string" &&
    name.length > 0 &&
    name.length <= MAX_FUNCTION_NAME_LEN &&
    FUNCTION_NAME_PATTERN.test(name)
  );
}

interface McpContentBlock {
  type: string;
  text?: string;
}

let nextRequestId = 1;

async function rpc(method: string, params?: unknown, toolName?: string, signal?: AbortSignal): Promise<any> {
  // Mcp-Method and Mcp-Name are required from 2026-07-28 onward so proxies can
  // route without parsing the body. The server rejects a mismatch.
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "Mcp-Protocol-Version": MCP_PROTOCOL_VERSION,
    "Mcp-Method": method,
    ...playgroundSessionHeaders,
  };
  if (toolName) {
    headers["Mcp-Name"] = toolName;
  }

  const response = await fetch(MCP_ENDPOINT, {
    method: "POST",
    headers,
    body: JSON.stringify({
      jsonrpc: "2.0",
      id: nextRequestId++,
      method,
      ...(params === undefined ? {} : { params }),
    }),
    signal,
  });

  if (response.status === 503) {
    throw new McpUnavailableError();
  }
  if (!response.ok) {
    throw new Error(`MCP transport error: ${response.status} - ${await response.text()}`);
  }

  const body = await response.json();
  if (body?.error) {
    throw new McpError(body.error.code, body.error.message ?? `MCP ${method} failed`);
  }
  return body?.result;
}

/**
 * Lists the server's tools, mapped into the OpenAI-shaped definitions the chat
 * completions API expects.
 *
 * Returns [] when the server has nothing indexed or speaks a different MCP
 * revision, so agent mode simply stays off rather than erroring at the user.
 */
export async function fetchToolDefinitions(signal?: AbortSignal): Promise<ToolDefinition[]> {
  let result: { tools?: McpTool[] } | undefined;
  try {
    result = await rpc("tools/list", undefined, undefined, signal);
  } catch (error) {
    if (error instanceof McpUnavailableError) return [];
    if (error instanceof McpError && error.code === CODE_UNSUPPORTED_PROTOCOL_VERSION) return [];
    throw error;
  }

  if (!Array.isArray(result?.tools)) return [];

  const usable = result.tools.filter((tool) => isUsableToolName(tool?.name));
  const dropped = result.tools.length - usable.length;
  if (dropped > 0) {
    console.warn(
      `Ignoring ${dropped} MCP tool(s) whose names cannot be used as function names:`,
      result.tools.filter((tool) => !isUsableToolName(tool?.name)).map((tool) => tool?.name)
    );
  }

  return usable
    .map((tool) => ({
      type: "function" as const,
      function: {
        name: tool.name,
        description: tool.description ?? tool.title ?? "",
        parameters: tool.inputSchema ?? { type: "object", properties: {} },
        title: tool.title?.trim() || undefined,
      },
    }));
}

/** Separator the registry uses between a provider ID and a tool's local name. */
const NAME_SEPARATOR = "__";

/**
 * A human-friendly label for a tool.
 *
 * Prefers the MCP tool's `title`. Falls back to humanizing the name: registry
 * names arrive namespaced as `provider__local_name`, so drop the provider
 * prefix and title-case the rest ("docs__search_docs" -> "Search Docs").
 */
export function friendlyToolName(name: string, title?: string): string {
  if (title && title.trim()) return title.trim();
  const sep = name.indexOf(NAME_SEPARATOR);
  const local = sep === -1 ? name : name.slice(sep + NAME_SEPARATOR.length);
  const words = local.split(/[_\-.]+/).filter(Boolean);
  if (words.length === 0) return name;
  return words.map((w) => w.charAt(0).toUpperCase() + w.slice(1)).join(" ");
}

/**
 * Calls one tool and returns its text.
 *
 * A tool that ran but could not answer -- an unknown doc id, a search with no
 * matches -- comes back as a normal result with isError set, carrying its own
 * explanation for the model. Only protocol failures throw.
 */
export async function callTool(name: string, args: unknown, signal?: AbortSignal): Promise<ToolCallOutcome> {
  const result = await rpc("tools/call", { name, arguments: args ?? {} }, name, signal);

  const blocks: McpContentBlock[] = Array.isArray(result?.content) ? result.content : [];
  const text = blocks
    .filter((block) => block?.type === "text" && typeof block.text === "string")
    .map((block) => block.text as string)
    .join("\n");

  return { text, isError: result?.isError === true };
}
