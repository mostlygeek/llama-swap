<script lang="ts">
  interface Props {
    content: string;
  }

  let { content }: Props = $props();
</script>

<div class="relative group inline-block">
  <span class="cursor-help">&#9432;</span>
  <div
    class="absolute top-full left-1/2 transform -translate-x-1/2 mt-2
           px-3 py-2 bg-gray-900 text-white text-sm rounded-md
           opacity-0 group-hover:opacity-100 transition-opacity
           duration-200 pointer-events-none whitespace-nowrap z-50 normal-case"
  >
    {content}
    <div class="absolute bottom-full left-1/2 transform -translate-x-1/2 border-4 border-transparent border-b-gray-900"></div>
  </div>
</div>
