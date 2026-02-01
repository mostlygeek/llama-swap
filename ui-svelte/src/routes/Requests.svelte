<script lang="ts">
  import { requests, getRequestDetail } from "../stores/api";
  import { type RequestLog, getTextContent } from "../lib/types";
  import JsonView from "../components/JsonView.svelte";
  import { ChevronDown, ChevronRight, Terminal, Brain, MessageSquare, Wrench } from "lucide-svelte";
  import { isNarrow } from "../stores/theme";
  import ResizablePanels from "../components/ResizablePanels.svelte";
  import { push, querystring } from "svelte-spa-router";

  let detailedRequest = $state<RequestLog | null>(null);
  let isLoadingDetail = $state(false);
  let showFullJson = $state(false);

  // Single source of truth for selection is the URL
  let selectedId = $derived.by(() => {
    const params = new URLSearchParams($querystring);
    const id = params.get("id");
    if (!id) return null;
    const n = parseInt(id) - 1;
    return isNaN(n) ? null : n;
  });

  // Fetch details whenever the selectedId changes
  $effect(() => {
    if (selectedId !== null) {
      if (!detailedRequest || detailedRequest.id !== selectedId) {
        loadDetail(selectedId);
      }
    } else {
      detailedRequest = null;
    }
  });

  async function loadDetail(id: number) {
    isLoadingDetail = true;
    try {
      detailedRequest = await getRequestDetail(id);
    } catch (err) {
      console.error(err);
    } finally {
      isLoadingDetail = false;
    }
  }

  let direction = $derived<"horizontal" | "vertical">($isNarrow ? "vertical" : "horizontal");

  // Track collapsed state for cards by ID/Index
  let collapsedCards = $state<Record<string, boolean>>({});

  function toggleCollapse(cardId: string) {
    collapsedCards[cardId] = !collapsedCards[cardId];
  }

  let selectedRequest = $derived.by(() => {
    if (selectedId === null) return null;
    const fromList = $requests.find((r) => r.id === selectedId);
    
    // If we don't even have the list item yet (e.g. direct link to old request), 
    // use the detailed fetch result.
    if (!fromList) return detailedRequest && detailedRequest.id === selectedId ? detailedRequest : null;

    // Use fromList as the base for real-time status/pending/duration
    const merged = { ...fromList };

    // If we have detailed data, fill in bodies if the list version is stripped (empty)
    // or shorter (e.g. fetch was more recent than the last SSE chunk we processed).
    if (detailedRequest && detailedRequest.id === selectedId) {
      if (!merged.request_body || (detailedRequest.request_body && detailedRequest.request_body.length > merged.request_body.length)) {
        merged.request_body = detailedRequest.request_body;
      }
      if (!merged.response_body || (detailedRequest.response_body && detailedRequest.response_body.length > merged.response_body.length)) {
        merged.response_body = detailedRequest.response_body;
      }
    }
    
    return merged;
  });

  function viewDetail(req: RequestLog) {
    // Only update the URL. The $effect above will handle the rest.
    push(`/requests?id=${req.id + 1}`);
  }

  function closeDetail() {
    push("/requests");
  }

  function formatDuration(ns: number): string {
    const ms = ns / 1000000;
    if (ms < 1000) return ms.toFixed(2) + "ms";
    return (ms / 1000).toFixed(2) + "s";
  }

  function formatRelativeTime(timestamp: string): string {
    const now = new Date();
    const date = new Date(timestamp);
    const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);
    if (diffInSeconds < 5) return "now";
    if (diffInSeconds < 60) return `${diffInSeconds}s ago`;
    const diffInMinutes = Math.floor(diffInSeconds / 60);
    if (diffInMinutes < 60) return `${diffInMinutes}m ago`;
    const diffInHours = Math.floor(diffInMinutes / 60);
    if (diffInHours < 24) return `${diffInHours}h ago`;
    return date.toLocaleString();
  }

  function parseRequestParts(req: RequestLog) {
    if (!req.request_body) return [];
    try {
      const body = JSON.parse(req.request_body);
      const parts: { type: string; text: string; icon?: any; tools?: any[]; name?: string; args?: string }[] = [];

      if (body.messages && Array.isArray(body.messages)) {
        body.messages.forEach((m: any) => {
          const role = m.role || "unknown";
          if (m.reasoning_content) {
            parts.push({ type: role + " reasoning", text: m.reasoning_content, icon: Brain });
          }
          if (m.content) {
            parts.push({
              type: role === "tool" ? "tool result" : role + " message",
              text: getTextContent(m.content),
              icon: role === "tool" ? Wrench : MessageSquare
            });
          }
          if (m.tool_calls && Array.isArray(m.tool_calls)) {
            m.tool_calls.forEach((tc: any) => {
              parts.push({
                type: "tool call",
                text: "",
                name: tc.function?.name || tc.name,
                args: tc.function?.arguments || tc.arguments,
                icon: Wrench
              });
            });
          }
        });
      } else if (body.prompt) {
        parts.push({ type: "prompt", text: body.prompt, icon: Terminal });
      } else if (body.input) {
        parts.push({
          type: "input",
          text: typeof body.input === "string" ? body.input : JSON.stringify(body.input, null, 2),
          icon: Terminal
        });
      }

      if (body.tools && Array.isArray(body.tools)) {
        const tools = body.tools.map((t: any) => ({
          name: t.function?.name || t.name,
          description: t.function?.description || t.description,
          parameters: t.function?.parameters || t.parameters
        }));
        parts.push({ type: "tools", text: "", tools, icon: Wrench });
      } else if (body.functions && Array.isArray(body.functions)) {
        const tools = body.functions.map((f: any) => ({
          name: f.name,
          description: f.description,
          parameters: f.parameters
        }));
        parts.push({ type: "functions", text: "", tools, icon: Wrench });
      }

      return parts;
    } catch (e) {
      return [];
    }
  }

  function parseResponseParts(req: RequestLog) {
    if (!req.response_body) return [];
    
    // Check if it's a streaming response (SSE)
    if (req.response_body.includes("data: ")) {
      const parts: { type: string; text: string; name?: string; args?: string; icon?: any }[] = [];
      let currentPart: { type: string; text: string; name?: string; args?: string; icon?: any } | null = null;

      const lines = req.response_body.split("\n");
      for (let line of lines) {
        line = line.trim();
        if (!line.startsWith("data:")) continue;
        const jsonStr = line.replace(/^data:\s*/, "");
        if (jsonStr === "[DONE]") continue;

        try {
          const chunk = JSON.parse(jsonStr);
          const delta = chunk.choices?.[0]?.delta;
          if (!delta) {
            // Fallback for non-delta
            const content = chunk.choices?.[0]?.text || chunk.choices?.[0]?.message?.content || chunk.content || chunk.text;
            if (content) {
              if (!currentPart || currentPart.type !== "content") {
                currentPart = { type: "content", text: "", icon: MessageSquare };
                parts.push(currentPart);
              }
              currentPart.text += content;
            }
            continue;
          }

          if (delta.reasoning_content) {
            if (!currentPart || currentPart.type !== "thought") {
              currentPart = { type: "thought", text: "", icon: Brain };
              parts.push(currentPart);
            }
            currentPart.text += delta.reasoning_content;
          } else if (delta.content) {
            if (!currentPart || currentPart.type !== "content") {
              currentPart = { type: "content", text: "", icon: MessageSquare };
              parts.push(currentPart);
            }
            currentPart.text += delta.content;
          } else if (delta.tool_calls) {
            for (const tc of delta.tool_calls) {
              if (tc.function?.name) {
                currentPart = { type: "tool call", text: "", name: tc.function.name, args: "", icon: Wrench };
                parts.push(currentPart);
              }
              if (tc.function?.arguments) {
                if (currentPart && currentPart.type === "tool call") {
                  currentPart.args += tc.function.arguments;
                }
              }
            }
          }
        } catch (e) {
          // ignore parse errors for chunks
        }
      }
      return parts;
    }

    // Check for "Tool: ... Args: ..." pattern in the raw response body
    if (req.response_body.includes("Tool: ") && req.response_body.includes("Args: ")) {
      const parts: { type: string; text: string; name?: string; args?: string; icon?: any }[] = [];
      const lines = req.response_body.split("\n");
      let currentTool: { type: string; text: string; name?: string; args?: string; icon?: any } | null = null;

      for (const line of lines) {
        if (line.startsWith("Tool: ")) {
          currentTool = { type: "tool", text: "", name: line.substring(6).trim(), args: "", icon: Wrench };
          parts.push(currentTool);
        } else if (line.startsWith("Args: ") && currentTool) {
          currentTool.args = line.substring(6).trim();
        } else if (currentTool && currentTool.type === "tool") {
          currentTool.args += line + "\n";
        } else if (line.trim()) {
          parts.push({ type: "content", text: line.trim(), icon: MessageSquare });
        }
      }
      return parts;
    }

    // Non-streaming JSON response
    try {
      const body = JSON.parse(req.response_body);
      const parts: { type: string; text: string; name?: string; args?: string; icon?: any }[] = [];
      
      const choice = body.choices?.[0];
      if (choice) {
        if (choice.message?.reasoning_content) {
          parts.push({ type: "thought", text: choice.message.reasoning_content, icon: Brain });
        }
        if (choice.message?.content) {
          parts.push({ type: "content", text: getTextContent(choice.message.content), icon: MessageSquare });
        } else if (choice.text) {
          parts.push({ type: "content", text: getTextContent(choice.text), icon: MessageSquare });
        }
        
        if (choice.message?.tool_calls) {
          for (const tc of choice.message.tool_calls) {
            parts.push({ 
              type: "tool call", 
              text: "",
              name: tc.function?.name,
              args: tc.function?.arguments,
              icon: Wrench
            });
          }
        }
      } else if (body.content) {
        parts.push({ type: "content", text: getTextContent(body.content), icon: MessageSquare });
      } else if (body.text) {
        parts.push({ type: "content", text: getTextContent(body.text), icon: MessageSquare });
      }
      
      return parts;
    } catch (e) {
      return [{ type: "content", text: req.response_body, icon: MessageSquare }];
    }
  }

  let sortedRequests = $derived([...$requests].sort((a, b) => b.id - a.id));
</script>

<div class="p-2 h-full flex flex-col">
  <h1 class="text-2xl font-bold mb-4">Requests</h1>

  <div class="flex-1 min-h-0 overflow-hidden">
    <ResizablePanels {direction} storageKey="requests-panel-group">
      {#snippet leftPanel()}
        <div class="h-full overflow-auto card">
          <table class="min-w-full divide-y">
            <thead class="border-gray-200 dark:border-white/10">
              <tr class="text-left text-xs uppercase tracking-wider">
                <th class="px-4 py-3">ID</th>
                <th class="px-4 py-3">Time</th>
                <th class="px-4 py-3">Method</th>
                <th class="px-4 py-3">Path</th>
                <th class="px-4 py-3">Model</th>
                <th class="px-4 py-3">Status</th>
                <th class="px-4 py-3">Duration</th>
              </tr>
            </thead>
            <tbody class="divide-y">
              {#each sortedRequests as req (req.id)}
                <tr
                  class="whitespace-nowrap text-sm cursor-pointer hover:bg-gray-100 dark:hover:bg-gray-800 {selectedId === req.id ? 'bg-gray-100 dark:bg-gray-800' : ''}"
                  onclick={() => viewDetail(req)}
                >
                  <td class="px-4 py-3">{req.id + 1}</td>
                  <td class="px-4 py-3">{formatRelativeTime(req.timestamp)}</td>
                  <td class="px-4 py-3 font-mono">{req.method}</td>
                  <td class="px-4 py-3 font-mono text-xs">{req.path}</td>
                  <td class="px-4 py-3">{req.model}</td>
                  <td class="px-4 py-3">
                    {#if req.pending}
                      <span class="text-yellow-500">pending</span>
                    {:else}
                      <span class={req.status >= 200 && req.status < 300 ? 'text-green-500' : 'text-red-500'}>
                        {req.status}
                      </span>
                    {/if}
                  </td>
                  <td class="px-4 py-3 text-gray-500">{req.pending ? "-" : formatDuration(req.duration)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/snippet}

      {#snippet rightPanel()}
        {#if selectedRequest}
          <div class="h-full overflow-auto card p-4 flex flex-col gap-4">
            <div class="flex justify-between items-center border-b pb-2 dark:border-white/10">
              <div class="flex items-baseline gap-4">
                <h2 class="text-lg font-bold">Request Detail #{selectedRequest.id + 1}</h2>
                <label class="flex items-center gap-2 text-xs font-normal cursor-pointer opacity-70 hover:opacity-100 transition-opacity">
                  <input type="checkbox" bind:checked={showFullJson} class="rounded border-gray-300 mt-[1px]" />
                  Show Full JSON
                </label>
              </div>
              <button class="text-gray-500 hover:text-black dark:hover:text-white" onclick={closeDetail}>✕</button>
            </div>

            {#if isLoadingDetail}
              <div class="flex justify-center py-8 italic text-gray-500 text-sm">Loading details...</div>
            {:else}
              <div class="space-y-4">
                {#if showFullJson}
                  {#if selectedRequest.request_body}
                    <div>
                      <h3 class="text-xs font-bold uppercase text-gray-500 mb-1">Request Body (JSON)</h3>
                      <JsonView content={selectedRequest.request_body} />
                    </div>
                  {/if}

                  {#if selectedRequest.response_body}
                    <div>
                      <h3 class="text-xs font-bold uppercase text-gray-500 mb-1">Response Body (Raw/SSE)</h3>
                      <JsonView content={selectedRequest.response_body} />
                    </div>
                  {/if}
                {:else}
                  {@const reqParts = parseRequestParts(selectedRequest)}
                  {#if reqParts.length > 0}
                    <div class="flex flex-col gap-4">
                      {#each reqParts as part, i}
                        {@const cardId = `req-${selectedRequest.id}-${i}`}
                        {@const isCollapsed = collapsedCards[cardId]}
                        <div class="card shadow-sm border border-border overflow-hidden">
                          <button 
                            class="w-full flex items-center justify-between p-3 bg-gray-50/50 dark:bg-white/5 hover:bg-gray-100 dark:hover:bg-white/10 transition-colors text-left"
                            onclick={() => toggleCollapse(cardId)}
                          >
                            <div class="flex items-center gap-2">
                              {#if part.icon}
                                <part.icon size={14} class="text-gray-400" />
                              {/if}
                              <h3 class="text-[10px] font-bold uppercase text-gray-500">
                                {#if part.type === 'tool call'}
                                  TOOL CALL: {part.name}
                                {:else if part.type === 'tool result'}
                                  TOOL RESULT
                                {:else}
                                  {part.type}
                                {/if}
                              </h3>
                            </div>
                            {#if isCollapsed}
                              <ChevronRight size={14} class="text-gray-400" />
                            {:else}
                              <ChevronDown size={14} class="text-gray-400" />
                            {/if}
                          </button>
                          
                          {#if !isCollapsed}
                            <div class="p-3 border-t border-border" class:pl-[34px]={part.icon}>
                              {#if part.tools}
                                <div class="flex flex-col gap-4">
                                  {#each part.tools as tool}
                                    <div class="space-y-1">
                                      <div class="flex items-center gap-2">
                                        <span class="text-xs font-bold font-mono text-blue-500 dark:text-blue-400">{tool.name}</span>
                                      </div>
                                      {#if tool.description}
                                        <p class="text-xs text-gray-500 dark:text-gray-400">{tool.description}</p>
                                      {/if}
                                      {#if tool.parameters && tool.parameters.properties}
                                        <div class="mt-2 space-y-2">
                                          <h4 class="text-[10px] font-bold uppercase text-gray-400">Parameters</h4>
                                          <div class="border border-border rounded overflow-hidden">
                                            <table class="min-w-full divide-y divide-border text-[11px]">
                                              <thead class="bg-gray-50/50 dark:bg-white/5">
                                                <tr>
                                                  <th class="px-3 py-2 text-left font-bold text-gray-500">Name</th>
                                                  <th class="px-3 py-2 text-left font-bold text-gray-500">Type</th>
                                                  <th class="px-3 py-2 text-left font-bold text-gray-500">Description</th>
                                                </tr>
                                              </thead>
                                              <tbody class="divide-y divide-border">
                                                {#each Object.entries(tool.parameters.properties) as [propName, prop]}
                                                  <tr>
                                                    <td class="px-3 py-2 font-mono whitespace-nowrap">
                                                      {propName}
                                                      {#if tool.parameters.required?.includes(propName)}
                                                        <span class="text-red-500 ml-1" title="Required">*</span>
                                                      {/if}
                                                    </td>
                                                    <td class="px-3 py-2">
                                                      <span class="px-1.5 py-0.5 bg-gray-100 dark:bg-gray-800 rounded text-gray-600 dark:text-gray-400 border border-border">
                                                        {(prop as any).type || 'any'}
                                                      </span>
                                                    </td>
                                                    <td class="px-3 py-2 text-gray-500 dark:text-gray-400">
                                                      {(prop as any).description || '-'}
                                                    </td>
                                                  </tr>
                                                {/each}
                                              </tbody>
                                            </table>
                                          </div>
                                        </div>
                                      {:else if tool.parameters}
                                        <div class="mt-2">
                                          <h4 class="text-[10px] font-bold uppercase text-gray-400 mb-1">Parameters</h4>
                                          <JsonView content={JSON.stringify(tool.parameters)} />
                                        </div>
                                      {/if}
                                    </div>
                                  {/each}
                                </div>
                              {:else if part.type === 'tool call'}
                                {@const parsedArgs = (() => {
                                  try {
                                    return JSON.parse(part.args || "{}");
                                  } catch (e) {
                                    return null;
                                  }
                                })()}
                                {#if parsedArgs && typeof parsedArgs === 'object'}
                                  <div class="border border-border rounded overflow-hidden">
                                    <table class="min-w-full divide-y divide-border text-[11px]">
                                      <thead class="bg-gray-50/50 dark:bg-white/5">
                                        <tr>
                                          <th class="px-3 py-2 text-left font-bold text-gray-500">Argument</th>
                                          <th class="px-3 py-2 text-left font-bold text-gray-500">Value</th>
                                        </tr>
                                      </thead>
                                      <tbody class="divide-y divide-border">
                                        {#each Object.entries(parsedArgs) as [name, value]}
                                          <tr>
                                            <td class="px-3 py-2 font-mono text-blue-500 dark:text-blue-400 whitespace-nowrap">{name}</td>
                                            <td class="px-3 py-2 text-gray-700 dark:text-gray-300">
                                              {#if typeof value === 'object' && value !== null}
                                                <JsonView content={JSON.stringify(value)} />
                                              {:else}
                                                {value}
                                              {/if}
                                            </td>
                                          </tr>
                                        {/each}
                                      </tbody>
                                    </table>
                                  </div>
                                {:else}
                                  <JsonView content={part.args || ""} />
                                {/if}
                                {:else}
                                <div class="text-sm whitespace-pre-wrap font-sans {part.type.includes('reasoning') || part.type === 'thought' ? 'italic text-gray-600 dark:text-gray-400' : ''}">
                                  {part.text}
                                </div>
                              {/if}
                            </div>
                          {/if}
                        </div>
                      {/each}
                    </div>
                  {:else if selectedRequest.request_body}
                    <div>
                      <h3 class="text-xs font-bold uppercase text-gray-500 mb-1">Request Body</h3>
                      <JsonView content={selectedRequest.request_body} />
                    </div>
                  {/if}

                  {@const respParts = parseResponseParts(selectedRequest)}
                  {#if respParts.length > 0}
                    <div class="flex flex-col gap-4">
                      {#each respParts as part, i}
                        {@const cardId = `resp-${selectedRequest.id}-${i}`}
                        {@const isCollapsed = collapsedCards[cardId]}
                        <div class="card shadow-sm border border-border overflow-hidden">
                          <button 
                            class="w-full flex items-center justify-between p-3 bg-gray-50/50 dark:bg-white/5 hover:bg-gray-100 dark:hover:bg-white/10 transition-colors text-left"
                            onclick={() => toggleCollapse(cardId)}
                          >
                             <div class="flex items-center gap-2">
                               {#if part.icon}
                                 <part.icon size={14} class="text-gray-400" />
                               {/if}
                               <h3 class="text-[10px] font-bold uppercase text-gray-500">
                                 {#if part.type === 'tool call'}
                                   TOOL CALL: {part.name}
                                 {:else if part.type === 'tool result'}
                                   TOOL RESULT
                                 {:else}
                                   {part.type}
                                 {/if}
                               </h3>
                             </div>
                            <div class="flex items-center gap-2">
                              {#if selectedRequest.pending && i === respParts.length - 1}
                                <span class="animate-pulse text-[10px]">●</span>
                              {/if}
                              {#if isCollapsed}
                                <ChevronRight size={14} class="text-gray-400" />
                              {:else}
                                <ChevronDown size={14} class="text-gray-400" />
                              {/if}
                            </div>
                          </button>

                          {#if !isCollapsed}
                            <div class="p-3 border-t border-border" class:pl-[34px]={part.icon}>
                              {#if part.type === 'tool call'}
                                {@const parsedArgs = (() => {
                                  try {
                                    return JSON.parse(part.args || "{}");
                                  } catch (e) {
                                    return null;
                                  }
                                })()}
                                {#if parsedArgs && typeof parsedArgs === 'object'}
                                  <div class="border border-border rounded overflow-hidden">
                                    <table class="min-w-full divide-y divide-border text-[11px]">
                                      <thead class="bg-gray-50/50 dark:bg-white/5">
                                        <tr>
                                          <th class="px-3 py-2 text-left font-bold text-gray-500">Argument</th>
                                          <th class="px-3 py-2 text-left font-bold text-gray-500">Value</th>
                                        </tr>
                                      </thead>
                                      <tbody class="divide-y divide-border">
                                        {#each Object.entries(parsedArgs) as [name, value]}
                                          <tr>
                                            <td class="px-3 py-2 font-mono text-blue-500 dark:text-blue-400 whitespace-nowrap">{name}</td>
                                            <td class="px-3 py-2 text-gray-700 dark:text-gray-300">
                                              {#if typeof value === 'object' && value !== null}
                                                <JsonView content={JSON.stringify(value)} />
                                              {:else}
                                                {value}
                                              {/if}
                                            </td>
                                          </tr>
                                        {/each}
                                      </tbody>
                                    </table>
                                  </div>
                                {:else}
                                  <JsonView content={part.args || ""} />
                                {/if}
                              {:else}
                                <div class="text-sm whitespace-pre-wrap font-sans {part.type === 'thought' ? 'italic text-gray-600 dark:text-gray-400' : ''}">
                                  {part.text}
                                </div>
                              {/if}
                            </div>
                          {/if}
                        </div>
                      {/each}
                    </div>
                  {:else if selectedRequest.response_body}
                    <div>
                      <h3 class="text-xs font-bold uppercase text-gray-500 mb-1">Response Body</h3>
                      <JsonView content={selectedRequest.response_body} />
                    </div>
                  {/if}
                {/if}
              </div>
            {/if}
          </div>
        {:else}
          <div class="h-full flex items-center justify-center card text-gray-400 italic">
            Select a request to view details
          </div>
        {/if}
      {/snippet}
    </ResizablePanels>
  </div>
</div>
