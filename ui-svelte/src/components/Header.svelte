<script lang="ts">
  import { link } from "svelte-spa-router";
  import { screenWidth, toggleTheme, isDarkMode, appTitle, isNarrow } from "../stores/theme";
  import { currentRoute } from "../stores/route";
  import { playgroundActivity } from "../stores/playgroundActivity";
  import ConnectionStatus from "./ConnectionStatus.svelte";

  function handleTitleChange(newTitle: string): void {
    const sanitized = newTitle.replace(/\n/g, "").trim().substring(0, 64) || "llama-swap";
    appTitle.set(sanitized);
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (e.key === "Enter") {
      e.preventDefault();
      const target = e.currentTarget as HTMLElement;
      handleTitleChange(target.textContent || "(set title)");
      target.blur();
    }
  }

  function handleBlur(e: FocusEvent): void {
    const target = e.currentTarget as HTMLElement;
    handleTitleChange(target.textContent || "(set title)");
  }

  function isActive(path: string, current: string): boolean {
    return path === "/" ? current === "/" : current.startsWith(path);
  }

  function goToPlayground(e: MouseEvent) {
    e.preventDefault();
    window.history.pushState({}, "", "/");
    currentRoute.set("/");
  }
</script>

<header
  class="flex items-center justify-between bg-surface border-b border-border px-4 {$isNarrow
    ? 'py-1 h-[60px]'
    : 'p-2 h-[75px]'}"
>
  {#if $screenWidth !== "xs" && $screenWidth !== "sm"}
    <h1
      contenteditable="true"
      class="p-0 outline-none hover:bg-gray-100 dark:hover:bg-gray-700 rounded"
      onblur={handleBlur}
      onkeydown={handleKeyDown}
    >
      {$appTitle}
    </h1>
  {/if}

  <menu class="flex items-center gap-4 overflow-x-auto">
    <a
      href="/"
      onclick={goToPlayground}
      class="p-1 whitespace-nowrap {isActive('/', $currentRoute) ? 'font-semibold' : ''} {$playgroundActivity ? 'activity-link' : 'text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100'}"
    >
      Playground
    </a>
    <a
      href="/models"
      use:link
      class="text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1 whitespace-nowrap"
      class:font-semibold={isActive("/models", $currentRoute)}
    >
      Models
    </a>
    <a
      href="/activity"
      use:link
      class="text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1 whitespace-nowrap"
      class:font-semibold={isActive("/activity", $currentRoute)}
    >
      Activity
    </a>
    <a
      href="/logs"
      use:link
      class="text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1 whitespace-nowrap"
      class:font-semibold={isActive("/logs", $currentRoute)}
    >
      Logs
    </a>
    <button onclick={toggleTheme} title="Toggle theme">
      {#if $isDarkMode}
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
          <path
            fill-rule="evenodd"
            d="M9.528 1.718a.75.75 0 0 1 .162.819A8.97 8.97 0 0 0 9 6a9 9 0 0 0 9 9 8.97 8.97 0 0 0 3.463-.69.75.75 0 0 1 .981.98 10.503 10.503 0 0 1-9.694 6.46c-5.799 0-10.5-4.7-10.5-10.5 0-4.368 2.667-8.112 6.46-9.694a.75.75 0 0 1 .818.162Z"
            clip-rule="evenodd"
          />
        </svg>
      {:else}
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
          <path
            d="M12 2.25a.75.75 0 0 1 .75.75v2.25a.75.75 0 0 1-1.5 0V3a.75.75 0 0 1 .75-.75ZM7.5 12a4.5 4.5 0 1 1 9 0 4.5 4.5 0 0 1-9 0ZM18.894 6.166a.75.75 0 0 0-1.06-1.06l-1.591 1.59a.75.75 0 1 0 1.06 1.061l1.591-1.59ZM21.75 12a.75.75 0 0 1-.75.75h-2.25a.75.75 0 0 1 0-1.5H21a.75.75 0 0 1 .75.75ZM17.834 18.894a.75.75 0 0 0 1.06-1.06l-1.59-1.591a.75.75 0 1 0-1.061 1.06l1.591 1.591ZM12 18a.75.75 0 0 1 .75.75V21a.75.75 0 0 1-1.5 0v-2.25A.75.75 0 0 1 12 18ZM7.758 17.303a.75.75 0 0 0-1.061-1.06l-1.591 1.59a.75.75 0 0 0 1.06 1.061l1.591-1.59ZM6 12a.75.75 0 0 1-.75.75H3a.75.75 0 0 1 0-1.5h2.25A.75.75 0 0 1 6 12ZM6.697 7.757a.75.75 0 0 0 1.06-1.06l-1.59-1.591a.75.75 0 0 0-1.061 1.06l1.59 1.591Z"
          />
        </svg>
      {/if}
    </button>
    <ConnectionStatus />
  </menu>
</header>

<style>
  .activity-link {
    font-weight: 600;
    background: linear-gradient(90deg, #f472b6, #c084fc, #60a5fa, #34d399, #fbbf24, #f472b6);
    background-size: 300% 100%;
    -webkit-background-clip: text;
    background-clip: text;
    -webkit-text-fill-color: transparent;
    animation: gradient-shift 3s linear infinite;
    text-shadow: 0 0 20px rgba(192, 132, 252, 0.5);
    filter: drop-shadow(0 0 8px rgba(192, 132, 252, 0.4));
  }

  @keyframes gradient-shift {
    0% {
      background-position: 0% 50%;
    }
    100% {
      background-position: 300% 50%;
    }
  }
</style>
