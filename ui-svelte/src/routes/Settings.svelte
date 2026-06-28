<script lang="ts">
  import { connectionState } from "../stores/theme";
  import { versionInfo } from "../stores/api";
</script>

<div class="p-2">
  <div class="mt-4 mb-4">
    <h3 class="text-lg font-semibold">Settings</h3>
  </div>

  <div class="rounded-lg border p-4 space-y-2 max-w-md">
    <h4 class="text-sm font-semibold text-muted-foreground">Build Information</h4>
    <dl class="text-sm space-y-1">
      <div class="flex justify-between gap-4">
        <dt class="text-muted-foreground">Event Stream</dt>
        <dd class="font-medium">{$connectionState ?? "unknown"}</dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-muted-foreground">API Version</dt>
        <dd class="font-medium">{$versionInfo?.version ?? "unknown"}</dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-muted-foreground">Commit Hash</dt>
        <dd class="font-medium">{$versionInfo?.commit?.substring(0, 7) ?? "unknown"}</dd>
      </div>
      <div class="flex justify-between gap-4">
        <dt class="text-muted-foreground">Build Date</dt>
        <dd class="font-medium">{$versionInfo?.build_date ?? "unknown"}</dd>
      </div>
    </dl>
  </div>
</div>
