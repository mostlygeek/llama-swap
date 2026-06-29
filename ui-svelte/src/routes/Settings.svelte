<script lang="ts">
  import { connectionState, themeName, themeMode, themes, type ThemeMode } from "../stores/theme";
  import { versionInfo } from "../stores/api";
  import { apiKey } from "../stores/auth";
  import * as Select from "$lib/components/ui/select/index.js";
  import { Input } from "$lib/components/ui/input/index.js";

  const modes: { value: ThemeMode; label: string }[] = [
    { value: "light", label: "Light" },
    { value: "dark", label: "Dark" },
    { value: "system", label: "System" },
  ];

  let themeLabel = $derived(themes.find((t) => t.value === $themeName)?.label ?? "Default");
  let modeLabel = $derived(modes.find((m) => m.value === $themeMode)?.label ?? "System");
</script>

<div class="p-2">
  <div class="mt-4 mb-4">
    <h3 class="text-lg font-semibold">Settings</h3>
  </div>

  <div class="rounded-lg border p-4 space-y-3 max-w-md mb-4">
    <h4 class="text-sm font-semibold text-muted-foreground">Appearance</h4>
    <div class="flex items-center justify-between gap-4">
      <span class="text-sm">Theme</span>
      <Select.Root
        type="single"
        value={$themeName}
        onValueChange={(v) => v && themeName.set(v as typeof $themeName)}
      >
        <Select.Trigger class="w-40">{themeLabel}</Select.Trigger>
        <Select.Content>
          {#each themes as theme (theme.value)}
            <Select.Item value={theme.value}>{theme.label}</Select.Item>
          {/each}
        </Select.Content>
      </Select.Root>
    </div>
    <div class="flex items-center justify-between gap-4">
      <span class="text-sm">Mode</span>
      <Select.Root
        type="single"
        value={$themeMode}
        onValueChange={(v) => v && themeMode.set(v as ThemeMode)}
      >
        <Select.Trigger class="w-40">{modeLabel}</Select.Trigger>
        <Select.Content>
          {#each modes as mode (mode.value)}
            <Select.Item value={mode.value}>{mode.label}</Select.Item>
          {/each}
        </Select.Content>
      </Select.Root>
    </div>
  </div>

  <div class="rounded-lg border p-4 space-y-3 max-w-md mb-4">
    <h4 class="text-sm font-semibold text-muted-foreground">Authentication</h4>
    <div class="flex flex-col gap-2">
      <span class="text-sm">Playground API Key</span>
      <Input type="password" placeholder="Optional API key for Playground endpoints" bind:value={$apiKey} />
    </div>
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
