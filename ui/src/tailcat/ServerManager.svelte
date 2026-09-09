<script lang="ts">
  import { get } from "svelte/store";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Label } from "$lib/components/ui/label/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Copy, Check, Pencil, Trash2, TriangleAlert, Plus } from "@lucide/svelte";
  import { copyText } from "$lib/clipboard";
  import {
    tailcatServers,
    activeServerID,
    activeFirst,
    defaultName,
    maskToken,
    newServerID,
    remove,
    upsert,
    type TailcatServer,
  } from "./servers";

  interface Props {
    /** "nodekey:..." for this browser, or "" while the module is still loading. */
    nodeKey: string;
    /** True when a saved client key could not be read and was replaced. */
    keyRegenerated: boolean;
    /** Activating a server means connecting to it and remembering the choice. */
    onactivate: (server: TailcatServer) => void;
  }

  let { nodeKey, keyRegenerated, onactivate }: Props = $props();

  function blank(): TailcatServer {
    return { id: newServerID(), name: "", token: "", apiKey: "", derpMapURL: "" };
  }

  // A draft is the add form when its id is not in the list yet, and the edit
  // form when it is. Null means the list is showing.
  //
  // With nothing saved there is no list worth showing, so the form opens
  // straight away. That is a decision about how things stood at mount, which is
  // why the list is read once with get() rather than through the $ store.
  let draft = $state<TailcatServer | null>(get(tailcatServers).length === 0 ? blank() : null);
  let copied = $state(false);

  let servers = $derived(activeFirst($tailcatServers, $activeServerID));
  let editing = $derived(draft !== null && $tailcatServers.some((server) => server.id === draft?.id));

  function normalized(server: TailcatServer): TailcatServer {
    return {
      ...server,
      name: server.name.trim() || defaultName(server.token.trim()),
      token: server.token.trim(),
      apiKey: server.apiKey.trim(),
      derpMapURL: server.derpMapURL.trim(),
    };
  }

  function save(activate: boolean) {
    if (!draft?.token.trim()) return;
    const server = normalized(draft);
    tailcatServers.update((list) => upsert(list, server));
    draft = null;
    if (activate) onactivate(server);
  }

  function deleteServer(id: string) {
    tailcatServers.update((list) => remove(list, id));
    if (get(activeServerID) === id) activeServerID.set("");
  }

  async function copyNodeKey() {
    if (!(await copyText(nodeKey))) return;
    copied = true;
    setTimeout(() => (copied = false), 1500);
  }
</script>

<div class="mx-auto flex w-full max-w-2xl flex-col gap-4 p-4">
  <div>
    <h1 class="text-2xl font-semibold">llama-swap Playground over Tailcat</h1>
    <p class="text-muted-foreground mt-1 text-sm">
      Connects to a llama-swap node started with <code>-listen-tailcat</code>. Nothing is sent to the
      site serving this page &mdash; the connection is made from your browser, over Tailcat.
    </p>
  </div>

  {#if draft}
    <Card.Root class="p-4">
      <Card.Header class="p-0">
        <Card.Title>{editing ? "Edit server" : "Add a server"}</Card.Title>
      </Card.Header>
      <Card.Content class="flex flex-col gap-3 p-0">
        <div class="flex flex-col gap-1.5">
          <Label for="tc-name">Name</Label>
          <Input id="tc-name" bind:value={draft.name} placeholder={defaultName(draft.token)} />
        </div>

        <div class="flex flex-col gap-1.5">
          <Label for="tc-token">Connection token</Label>
          <Input id="tc-token" bind:value={draft.token} placeholder="tc..." spellcheck={false} autocapitalize="off" />
          <p class="text-muted-foreground text-xs">
            Printed to the node's log at startup, and shown on the Tailcat page in its UI. Tokens are
            case-sensitive.
          </p>
        </div>

        <div class="flex flex-col gap-1.5">
          <Label for="tc-apikey">API key</Label>
          <Input id="tc-apikey" type="password" bind:value={draft.apiKey} autocomplete="off" />
          <p class="text-muted-foreground text-xs">
            Only needed when the node configures <code>apiKeys</code>. Leave empty otherwise.
          </p>
        </div>

        <div class="flex flex-col gap-1.5">
          <Label for="tc-derp">DERP map URL</Label>
          <Input id="tc-derp" bind:value={draft.derpMapURL} placeholder="https://tailcat.dev/derpmap.json" />
          <p class="text-muted-foreground text-xs">
            Optional. Tailcat's default public map is used when this is empty.
          </p>
        </div>

        <p class="text-muted-foreground flex items-start gap-2 text-xs">
          <TriangleAlert class="mt-0.5 size-3.5 shrink-0" />
          <span>
            The token and API key are both credentials, and saving them stores them unencrypted in
            this browser. Anyone with access to this device can read them.
          </span>
        </p>
      </Card.Content>
      <Card.Footer class="gap-2 p-0">
        <Button onclick={() => save(true)} disabled={!draft.token.trim()}>Save and connect</Button>
        <Button variant="outline" onclick={() => save(false)} disabled={!draft.token.trim()}>Save</Button>
        {#if $tailcatServers.length > 0}
          <Button variant="ghost" onclick={() => (draft = null)}>Cancel</Button>
        {/if}
      </Card.Footer>
    </Card.Root>
  {:else}
    <Card.Root class="p-4">
      <Card.Header class="p-0">
        <Card.Title>Servers</Card.Title>
        <Card.Description>
          Pick a server to connect to. The one you last used is kept at the top.
        </Card.Description>
      </Card.Header>
      <Card.Content class="flex flex-col gap-2 p-0">
        {#each servers as server (server.id)}
          {@const isActive = server.id === $activeServerID}
          <div class="flex items-center gap-2 rounded-lg border p-2" class:border-primary={isActive}>
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="truncate font-medium">{server.name}</span>
                {#if isActive}<Badge variant="secondary">Last used</Badge>{/if}
                {#if server.apiKey}<Badge variant="outline">API key</Badge>{/if}
              </div>
              <div class="text-muted-foreground truncate font-mono text-xs">{maskToken(server.token)}</div>
            </div>
            <Button size="sm" onclick={() => onactivate(server)}>Connect</Button>
            <Button size="sm" variant="ghost" aria-label="Edit {server.name}" onclick={() => (draft = { ...server })}>
              <Pencil class="size-4" />
            </Button>
            <Button
              size="sm"
              variant="ghost"
              aria-label="Delete {server.name}"
              onclick={() => deleteServer(server.id)}
            >
              <Trash2 class="size-4" />
            </Button>
          </div>
        {/each}
      </Card.Content>
      <Card.Footer class="p-0">
        <Button variant="outline" onclick={() => (draft = blank())}>
          <Plus class="size-4" />
          Add a server
        </Button>
      </Card.Footer>
    </Card.Root>
  {/if}

  <Card.Root class="p-4">
    <Card.Header class="p-0">
      <Card.Title class="text-base">This browser's node key</Card.Title>
    </Card.Header>
    <Card.Content class="flex flex-col gap-2 p-0">
      {#if nodeKey}
        <div class="flex items-center gap-2">
          <code class="bg-muted min-w-0 flex-1 truncate rounded px-2 py-1 text-xs">{nodeKey}</code>
          <Button size="sm" variant="outline" onclick={copyNodeKey} aria-label="Copy node key">
            {#if copied}<Check class="size-4" />{:else}<Copy class="size-4" />{/if}
          </Button>
        </div>
        <p class="text-muted-foreground text-xs">
          Add this to the node's <code>tailcat.allow</code> list when it restricts which clients may
          connect. It stays the same on this browser unless you clear its storage.
        </p>
        {#if keyRegenerated}
          <p class="text-destructive text-xs">
            The saved client key could not be read and a new one was generated. Any
            <code>tailcat.allow</code> list still naming the old key needs updating.
          </p>
        {/if}
      {:else}
        <p class="text-muted-foreground text-sm">Loading the Tailcat module&hellip;</p>
      {/if}
    </Card.Content>
  </Card.Root>
</div>
