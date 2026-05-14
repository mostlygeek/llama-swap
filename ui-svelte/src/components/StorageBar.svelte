<script lang="ts">
  import type { ResourcesResponse } from "../lib/types";

  let { resources }: { resources: ResourcesResponse | null } = $props();

  function fmt(bytes: number): string {
    if (bytes >= 1e12) return (bytes / 1e12).toFixed(1) + " TB";
    if (bytes >= 1e9) return (bytes / 1e9).toFixed(1) + " GB";
    if (bytes >= 1e6) return (bytes / 1e6).toFixed(0) + " MB";
    return bytes + " B";
  }

  let usedPct = $derived.by(() => {
    const s = resources?.storage;
    if (!s || s.total_bytes === 0) return 0;
    return Math.round((s.used_bytes / s.total_bytes) * 100);
  });

  let vramUsedPct = $derived.by(() => {
    const g = resources?.gpu?.[0];
    if (!g || g.vram_total_mb === 0) return 0;
    return Math.round((g.vram_used_mb / g.vram_total_mb) * 100);
  });

  let barColor = $derived(usedPct > 90 ? "bg-red-500" : usedPct > 75 ? "bg-yellow-500" : "bg-blue-500");
  let vramColor = $derived(vramUsedPct > 90 ? "bg-red-500" : vramUsedPct > 75 ? "bg-yellow-500" : "bg-green-500");
</script>

{#if resources}
  <div class="flex flex-col gap-1 text-xs text-txtsecondary pb-2 border-b border-gray-200 dark:border-white/10">
    {#if resources.storage}
      <div class="flex items-center gap-2">
        <span class="w-10 shrink-0">Disk</span>
        <div class="flex-1 bg-gray-200 dark:bg-white/10 rounded-full h-1.5">
          <div class="{barColor} h-1.5 rounded-full transition-all" style="width: {usedPct}%"></div>
        </div>
        <span class="w-24 text-right shrink-0">
          {fmt(resources.storage.used_bytes)} / {fmt(resources.storage.total_bytes)}
        </span>
      </div>
    {/if}
    {#if resources.gpu && resources.gpu.length > 0}
      {@const g = resources.gpu[0]}
      <div class="flex items-center gap-2">
        <span class="w-10 shrink-0" title={g.name}>{resources.memory?.type === "unified" ? "Mem" : "VRAM"}</span>
        <div class="flex-1 bg-gray-200 dark:bg-white/10 rounded-full h-1.5">
          <div class="{vramColor} h-1.5 rounded-full transition-all" style="width: {vramUsedPct}%"></div>
        </div>
        <span class="w-24 text-right shrink-0" title={g.name}>
          {g.vram_used_mb >= 1024 ? (g.vram_used_mb / 1024).toFixed(1) + " GB" : g.vram_used_mb + " MB"} / {g.vram_total_mb >= 1024 ? (g.vram_total_mb / 1024).toFixed(0) + " GB" : g.vram_total_mb + " MB"}
          {#if g.temp_c > 0}<span class="ml-1">{g.temp_c}°C</span>{/if}
        </span>
      </div>
    {/if}
  </div>
{/if}
