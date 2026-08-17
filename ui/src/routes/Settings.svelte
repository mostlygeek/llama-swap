<script lang="ts">
  import { connectionState, themeName, themeMode, themes, type ThemeMode } from "../stores/theme";
  import { versionInfo } from "../stores/api";
  import { showCapabilityTags } from "../stores/modelDisplay";
  import * as Select from "$lib/components/ui/select/index.js";
  import * as Switch from "$lib/components/ui/switch/index.js";
  import * as Label from "$lib/components/ui/label/index.js";

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
    <h4 class="text-sm font-semibold text-muted-foreground">Models page</h4>
    <div class="flex items-start justify-between gap-4">
      <div>
        <Label.Root for="show-capability-tags" class="text-sm">Show capability tags</Label.Root>
        <p class="text-muted-foreground text-xs">
          Show all capability badges next to each model, mirroring the Details tab
          (vision, tools, context window, and more).
        </p>
      </div>
      <Switch.Root
        id="show-capability-tags"
        checked={$showCapabilityTags}
        onCheckedChange={(v) => showCapabilityTags.set(v)}
      />
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
        <dt class="text-muted-foreground">Version</dt>
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
