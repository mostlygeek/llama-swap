<script lang="ts">
  import { onMount } from "svelte";
  import { getClusterStatus } from "../stores/api";
  import type { ClusterStatusState } from "../lib/types";
  import { collapseHomePath } from "../lib/pathDisplay";

  let loading = $state(true);
  let refreshing = $state(false);
  let error = $state<string | null>(null);
  let state = $state<ClusterStatusState | null>(null);
  let refreshController: AbortController | null = null;

  function formatTime(value?: string): string {
    if (!value) return "unknown";
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return parsed.toLocaleString();
  }

  function formatLatency(value?: number): string {
    if (value == null || value < 0) return "-";
    return `${value} ms`;
  }

  function overallClass(overall: ClusterStatusState["overall"]): string {
    switch (overall) {
      case "healthy":
        return "border-green-400/40 bg-green-600/15 text-green-300";
      case "solo":
        return "border-sky-400/40 bg-sky-600/15 text-sky-300";
      case "degraded":
        return "border-amber-400/40 bg-amber-600/15 text-amber-300";
      default:
        return "border-error/40 bg-error/10 text-error";
    }
  }

  async function refresh(): Promise<void> {
    refreshController?.abort();
    const controller = new AbortController();
    refreshController = controller;
    const timeout = setTimeout(() => controller.abort(), 30000);

    refreshing = true;
    error = null;
    if (!state) loading = true;
    try {
      state = await getClusterStatus(controller.signal);
    } catch (e) {
      if (controller.signal.aborted) {
        error = "Timeout al consultar el estado del cluster. Pulsa Refresh para reintentar.";
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
    } finally {
      clearTimeout(timeout);
      if (refreshController === controller) {
        refreshController = null;
      }
      refreshing = false;
      loading = false;
    }
  }

  onMount(() => {
    void refresh();
    return () => {
      refreshController?.abort();
    };
  });
</script>

<div class="h-full flex flex-col gap-2">
  <div class="card shrink-0">
    <div class="flex items-center justify-between gap-2">
      <h2 class="pb-0">Cluster</h2>
      <button class="btn btn--sm" onclick={refresh} disabled={refreshing}>
        {refreshing ? "Refreshing..." : "Refresh"}
      </button>
    </div>

    {#if state}
      <div class="mt-2 inline-flex items-center rounded border px-2 py-1 text-sm {overallClass(state.overall)}">
        {state.overall.toUpperCase()}
      </div>
      <div class="mt-2 text-sm text-txtsecondary">{state.summary}</div>
      <div class="mt-2 text-xs text-txtsecondary break-all">
        Backend:
        <span class="font-mono" title={state.backendDir}>{collapseHomePath(state.backendDir)}</span>
      </div>
      <div class="text-xs text-txtsecondary break-all">
        autodiscover.sh:
        <span class="font-mono" title={state.autodiscoverPath}>{collapseHomePath(state.autodiscoverPath)}</span>
      </div>
      <div class="text-xs text-txtsecondary">
        Última comprobación: {formatTime(state.detectedAt)}
      </div>
    {/if}

    {#if error}
      <div class="mt-2 p-2 border border-error/40 bg-error/10 rounded text-sm text-error break-words">{error}</div>
    {/if}
  </div>

  <div class="card flex-1 min-h-0 overflow-auto">
    {#if loading}
      <div class="text-sm text-txtsecondary">Comprobando conectividad del cluster...</div>
    {:else if state}
      <div class="grid grid-cols-1 md:grid-cols-3 gap-2 text-sm mb-3">
        <div class="rounded border border-card-border p-2">
          <div class="text-txtsecondary text-xs uppercase">Local IP</div>
          <div class="font-mono break-all">{state.localIp || "-"}</div>
          <div class="text-xs text-txtsecondary mt-1">CIDR: {state.cidr || "-"}</div>
        </div>
        <div class="rounded border border-card-border p-2">
          <div class="text-txtsecondary text-xs uppercase">Interfaces</div>
          <div class="font-mono break-all">ETH: {state.ethIf || "-"}</div>
          <div class="font-mono break-all">IB: {state.ibIf || "-"}</div>
        </div>
        <div class="rounded border border-card-border p-2">
          <div class="text-txtsecondary text-xs uppercase">Nodos</div>
          <div>Total: {state.nodeCount}</div>
          <div>Remotos: {state.remoteCount}</div>
          <div>SSH OK: {state.reachableBySsh}</div>
        </div>
      </div>

      {#if state.errors && state.errors.length > 0}
        <div class="mb-3 p-2 border border-amber-400/30 bg-amber-600/10 rounded">
          <div class="text-sm text-amber-300 font-semibold">Avisos de autodetección</div>
          <ul class="mt-1 text-sm text-amber-200 list-disc pl-5">
            {#each state.errors as line}
              <li>{line}</li>
            {/each}
          </ul>
        </div>
      {/if}

      <div class="overflow-auto border border-card-border rounded">
        <table class="w-full text-sm">
          <thead class="bg-surface">
            <tr>
              <th class="text-left p-2 border-b border-card-border">Nodo</th>
              <th class="text-left p-2 border-b border-card-border">Rol</th>
              <th class="text-left p-2 border-b border-card-border">Port 22</th>
              <th class="text-left p-2 border-b border-card-border">SSH BatchMode</th>
              <th class="text-left p-2 border-b border-card-border">Error</th>
            </tr>
          </thead>
          <tbody>
            {#each state.nodes as node}
              <tr>
                <td class="p-2 border-b border-card-border font-mono">{node.ip}</td>
                <td class="p-2 border-b border-card-border">{node.isLocal ? "local" : "remote"}</td>
                <td class="p-2 border-b border-card-border">
                  <span class={node.port22Open ? "text-green-300" : "text-error"}>
                    {node.port22Open ? "OK" : "FAIL"}
                  </span>
                  <span class="text-xs text-txtsecondary ml-1">({formatLatency(node.port22LatencyMs)})</span>
                </td>
                <td class="p-2 border-b border-card-border">
                  <span class={node.sshOk ? "text-green-300" : "text-error"}>
                    {node.sshOk ? "OK" : "FAIL"}
                  </span>
                  <span class="text-xs text-txtsecondary ml-1">({formatLatency(node.sshLatencyMs)})</span>
                </td>
                <td class="p-2 border-b border-card-border break-words">{node.error || "-"}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <div class="text-sm text-txtsecondary">No hay datos de cluster.</div>
    {/if}
  </div>
</div>
