<script lang="ts">
  import ChatInterface from "../components/playground/ChatInterface.svelte";
  import ImageInterface from "../components/playground/ImageInterface.svelte";
  import AudioInterface from "../components/playground/AudioInterface.svelte";
  import SpeechInterface from "../components/playground/SpeechInterface.svelte";
  import RerankInterface from "../components/playground/RerankInterface.svelte";
  import ConcurrencyInterface from "../components/playground/ConcurrencyInterface.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { selectedPlaygroundTab } from "../stores/playground";
</script>

<Card.Root class="flex h-full flex-col gap-0 overflow-hidden p-4">
  <!-- Tab content (all interfaces stay mounted); navigation via the sidebar -->
  <div class="relative flex-1 overflow-hidden">
    <div class="h-full" class:hidden={$selectedPlaygroundTab !== "chat"}>
      <ChatInterface />
    </div>
    <div class="h-full" class:hidden={$selectedPlaygroundTab !== "images"}>
      <ImageInterface />
    </div>
    <div class="h-full" class:hidden={$selectedPlaygroundTab !== "speech"}>
      <SpeechInterface />
    </div>
    <div class="h-full" class:hidden={$selectedPlaygroundTab !== "audio"}>
      <AudioInterface />
    </div>
    <div class="h-full" class:hidden={$selectedPlaygroundTab !== "rerank"}>
      <RerankInterface />
    </div>
    <div class="h-full" class:hidden={$selectedPlaygroundTab !== "concurrency"}>
      <ConcurrencyInterface />
    </div>
  </div>
</Card.Root>
