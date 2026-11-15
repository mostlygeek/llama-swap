<script lang="ts">
  import { onMount } from "svelte";
  import { push } from "svelte-spa-router";
  import { SquarePen, Pencil, Trash2, Ellipsis, Pin, PinOff } from "@lucide/svelte";
  import * as Sidebar from "$lib/components/ui/sidebar/index.js";
  import * as Dialog from "$lib/components/ui/dialog/index.js";
  import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { chatHistory, chatStorageError, listSavedChats, type SavedChat } from "../stores/chatHistory";
  import { selectedPlaygroundTab } from "../stores/playground";
  import { currentRoute } from "../stores/route";

  const generating = chatHistory.generating;
  const sidebar = Sidebar.useSidebar();
  let pageVisible = $state(document.visibilityState === "visible");
  onMount(() => {
    const updateVisibility = () => { pageVisible = document.visibilityState === "visible"; };
    document.addEventListener("visibilitychange", updateVisibility);
    return () => {
      document.removeEventListener("visibilitychange", updateVisibility);
      chatHistory.setViewed(null);
    };
  });
  $effect(() => {
    chatHistory.setViewed(pageVisible && $currentRoute === "/playground" && $selectedPlaygroundTab === "chat"
      ? $chatHistory.activeId : null);
  });
  let search = $state("");
  let editing = $state<SavedChat | null>(null);
  let deleting = $state<SavedChat | null>(null);
  let title = $state("");
  let visible = $derived(listSavedChats($chatHistory.chats, search));

  function showChat() {
    selectedPlaygroundTab.set("chat");
    push("/playground");
    if (sidebar.isMobile) sidebar.setOpenMobile(false);
  }
</script>

<Sidebar.Group class="group-data-[collapsible=icon]:hidden">
  <Sidebar.GroupLabel>Chats</Sidebar.GroupLabel>
  <Sidebar.GroupContent class="space-y-2">
    <Sidebar.Menu>
      <Sidebar.MenuItem>
        <Sidebar.MenuButton>
          {#snippet child({ props })}
            <button {...props} onclick={() => { chatHistory.create(); search = ""; showChat(); }}>
              <SquarePen /><span>New chat</span>
            </button>
          {/snippet}
        </Sidebar.MenuButton>
      </Sidebar.MenuItem>
    </Sidebar.Menu>
    <Input aria-label="Search chats" placeholder="Search chats…" bind:value={search} class="h-8" />
    <p class="px-2 text-xs text-muted-foreground">Saved in this browser</p>
    {#if $chatStorageError}
      <p role="alert" class="px-2 text-xs text-destructive">{$chatStorageError}</p>
    {/if}
    <Sidebar.Menu>
      {#each visible as chat (chat.id)}
        <Sidebar.MenuItem>
          <Sidebar.MenuButton
            class="data-active:bg-sidebar-accent" title={chat.title}
            isActive={$currentRoute === "/playground" && $selectedPlaygroundTab === "chat" && $chatHistory.activeId === chat.id}
          >
            {#snippet child({ props })}
              <button {...props} onclick={() => { chatHistory.select(chat.id); showChat(); }}>
                <span class:font-semibold={chat.unread} class="min-w-0 flex-1 truncate">{chat.title}</span>
                {#if chat.pinned}
                  <Pin class="size-3! text-muted-foreground" aria-label="Pinned chat" />
                {/if}
                {#if $generating.has(chat.id)}
                  <span class="ml-auto size-2 shrink-0 animate-pulse rounded-full bg-primary" role="status" aria-label="Generating response"></span>
                {:else if chat.unread}
                  <span class="ml-auto size-2 shrink-0 rounded-full bg-blue-500" role="status" aria-label="Unread response" title="New response — unread"></span>
                {/if}
              </button>
            {/snippet}
          </Sidebar.MenuButton>
          <DropdownMenu.Root>
            <Sidebar.MenuAction showOnHover={!sidebar.isMobile} class="[@media(hover:none)]:opacity-100!">
              {#snippet child({ props })}
                <DropdownMenu.Trigger {...props} aria-label={`Options for ${chat.title}`} title="Chat options">
                  <Ellipsis />
                </DropdownMenu.Trigger>
              {/snippet}
            </Sidebar.MenuAction>
            <DropdownMenu.Content
              side="bottom" align="end" avoidCollisions={true} collisionPadding={8}
              class="w-44 max-w-[calc(100vw-1rem)] max-h-[var(--bits-floating-available-height)]"
              onCloseAutoFocus={(event) => { if (editing || deleting) event.preventDefault(); }}
            >
              <DropdownMenu.Item onSelect={() => chatHistory.togglePin(chat.id)}>
                {#if chat.pinned}<PinOff />Unpin chat{:else}<Pin />Pin chat{/if}
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={() => { title = chat.title; editing = chat; }}>
                <Pencil />Rename
              </DropdownMenu.Item>
              <DropdownMenu.Separator />
              <DropdownMenu.Item variant="destructive" disabled={$generating.has(chat.id)}
                onSelect={() => { deleting = chat; }}>
                <Trash2 />Delete
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Root>
        </Sidebar.MenuItem>
      {:else}
        <li class="px-2 py-2 text-xs text-muted-foreground">{search.trim() ? "No matching chats." : "Your chats will appear here after you send a message."}</li>
      {/each}
    </Sidebar.Menu>
  </Sidebar.GroupContent>
</Sidebar.Group>

<Dialog.Root open={editing !== null} onOpenChange={(open) => { if (!open) editing = null; }}>
  <Dialog.Content class="sm:max-w-sm">
    <Dialog.Header><Dialog.Title>Rename chat</Dialog.Title></Dialog.Header>
    <form class="space-y-4" onsubmit={(event) => {
      event.preventDefault();
      if (editing && title.trim()) { chatHistory.rename(editing.id, title); editing = null; }
    }}>
      <Input aria-label="Chat title" bind:value={title} maxlength={80} />
      <Dialog.Footer>
        <Button type="button" variant="outline" onclick={() => editing = null}>Cancel</Button>
        <Button type="submit" disabled={!title.trim()}>Save</Button>
      </Dialog.Footer>
    </form>
  </Dialog.Content>
</Dialog.Root>

<Dialog.Root open={deleting !== null} onOpenChange={(open) => { if (!open) deleting = null; }}>
  <Dialog.Content class="sm:max-w-sm">
    <Dialog.Header>
      <Dialog.Title>Delete chat?</Dialog.Title>
      <Dialog.Description>“{deleting?.title}” will be removed from this browser. This cannot be undone.</Dialog.Description>
    </Dialog.Header>
    <Dialog.Footer>
      <Button variant="outline" onclick={() => deleting = null}>Cancel</Button>
      <Button variant="destructive" disabled={deleting !== null && $generating.has(deleting.id)} onclick={() => {
        if (deleting) chatHistory.remove(deleting.id);
        deleting = null;
      }}>Delete</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
