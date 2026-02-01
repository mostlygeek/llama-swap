<script lang="ts">
  let { content = "" } = $props();

  let formattedContent = $derived.by(() => {
    try {
      const obj = JSON.parse(content);
      return JSON.stringify(obj, null, 2);
    } catch (e) {
      return content;
    }
  });
</script>

<div class="relative group">
  <pre class="text-xs bg-gray-50 dark:bg-black/20 p-2 rounded overflow-x-auto border border-border font-mono whitespace-pre-wrap">{formattedContent}</pre>
</div>
