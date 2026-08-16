<script lang="ts">
  import { connectionState } from "../stores/theme";

  let eventStatusColor = $derived.by(() => {
    switch ($connectionState) {
      case "connected":
        return "bg-emerald-500";
      case "connecting":
        return "bg-amber-500";
      case "disconnected":
      default:
        return "bg-red-500";
    }
  });

  let tooltipText = $derived(`Event Stream: ${$connectionState ?? "unknown"}`);
</script>

<div class="flex items-center" title={tooltipText}>
  <span class="inline-block w-3 h-3 rounded-full {eventStatusColor} mr-2"></span>
</div>
