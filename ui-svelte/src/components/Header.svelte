<script lang="ts">
  import { link } from "svelte-spa-router";
  import { screenWidth, toggleTheme, themeMode, appTitle, isNarrow } from "../stores/theme";
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
      use:link
      class="p-1 whitespace-nowrap {isActive('/', $currentRoute) ? 'font-semibold underline underline-offset-4' : ''} {$playgroundActivity ? 'activity-link' : 'text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100'}"
    >
      Playground
    </a>
    <a
      href="/models"
      use:link
      class="text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1 whitespace-nowrap"
      class:font-semibold={isActive("/models", $currentRoute)}
      class:underline={isActive("/models", $currentRoute)}
      class:underline-offset-4={isActive("/models", $currentRoute)}
    >
      Models
    </a>
    <a
      href="/activity"
      use:link
      class="text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1 whitespace-nowrap"
      class:font-semibold={isActive("/activity", $currentRoute)}
      class:underline={isActive("/activity", $currentRoute)}
      class:underline-offset-4={isActive("/activity", $currentRoute)}
    >
      Activity
    </a>
    <a
      href="/logs"
      use:link
      class="text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1 whitespace-nowrap"
      class:font-semibold={isActive("/logs", $currentRoute)}
      class:underline={isActive("/logs", $currentRoute)}
      class:underline-offset-4={isActive("/logs", $currentRoute)}
    >
      Logs
    </a>
    <button onclick={toggleTheme} title="Toggle theme (current: {$themeMode})">
      {#if $themeMode === "system"}
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
          <path d="M0,9c0-.552,.448-1,1-1H3.108c.147-.874,.472-1.721,1.006-2.471l-1.478-1.478c-.391-.391-.391-1.023,0-1.414s1.023-.391,1.414,0l1.478,1.478c.751-.534,1.598-.859,2.471-1.006V1c0-.552,.448-1,1-1s1,.448,1,1V3.108c.874,.147,1.725,.466,2.477,1.001l1.473-1.473c.391-.391,1.023-.391,1.414,0s.391,1.023,0,1.414L3.963,15.45c-.195,.195-.451,.293-.707,.293s-.512-.098-.707-.293c-.391-.391-.391-1.023,0-1.414l1.56-1.56c-.535-.751-.854-1.602-1.001-2.477H1c-.552,0-1-.448-1-1ZM23.707,.293c-.391-.391-1.023-.391-1.414,0L.293,22.293c-.391,.391-.391,1.023,0,1.414,.195,.195,.451,.293,.707,.293s.512-.098,.707-.293L23.707,1.707c.391-.391,.391-1.023,0-1.414Zm-.283,10.954c.32-.15,.538-.458,.572-.81,.034-.353-.121-.696-.407-.904-.858-.625-1.833-1.066-2.897-1.315-.335-.078-.69,.022-.934,.267l-8.392,8.391c-.244,.244-.345,.597-.267,.933,.843,3.646,4.047,6.191,7.792,6.191,1.695,0,3.32-.53,4.697-1.533,.286-.208,.441-.553,.407-.904-.034-.353-.251-.66-.572-.811-1.842-.861-3.033-2.727-3.033-4.752s1.19-3.891,3.033-4.753Z"/>
        </svg>
      {:else if $themeMode === "light"}
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
          <path
            fill-rule="evenodd"
            d="M12 2.25a.75.75 0 0 1 .75.75v2.25a.75.75 0 0 1-1.5 0V3a.75.75 0 0 1 .75-.75ZM7.5 12a4.5 4.5 0 1 1 9 0 4.5 4.5 0 0 1-9 0ZM18.894 6.166a.75.75 0 0 0-1.06-1.06l-1.591 1.59a.75.75 0 1 0 1.06 1.061l1.591-1.59ZM21.75 12a.75.75 0 0 1-.75.75h-2.25a.75.75 0 0 1 0-1.5H21a.75.75 0 0 1 .75.75ZM17.834 18.894a.75.75 0 0 0 1.06-1.06l-1.59-1.591a.75.75 0 1 0-1.061 1.06l1.591 1.591ZM12 18a.75.75 0 0 1 .75.75V21a.75.75 0 0 1-1.5 0v-2.25A.75.75 0 0 1 12 18ZM7.758 17.303a.75.75 0 0 0-1.061-1.06l-1.591 1.59a.75.75 0 0 0 1.06 1.061l1.591-1.59ZM6 12a.75.75 0 0 1-.75.75H3a.75.75 0 0 1 0-1.5h2.25A.75.75 0 0 1 6 12ZM6.697 7.757a.75.75 0 0 0 1.06-1.06l-1.59-1.591a.75.75 0 0 0-1.061 1.06l1.59 1.591Z"
          />
        </svg>
      {:else}
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
          <path
            fill-rule="evenodd"
            d="M9.528 1.718a.75.75 0 0 1 .162.819A8.97 8.97 0 0 0 9 6a9 9 0 0 0 9 9 8.97 8.97 0 0 0 3.463-.69.75.75 0 0 1 .981.98 10.503 10.503 0 0 1-9.694 6.46c-5.799 0-10.5-4.7-10.5-10.5 0-4.368 2.667-8.112 6.46-9.694a.75.75 0 0 1 .818.162Z"
            clip-rule="evenodd"
          />
        </svg>
      {/if}
    </button>
    <ConnectionStatus />
  </menu>
</header>

<style>
  .activity-link {
    background: linear-gradient(90deg, #6366f1, #8b5cf6, #a855f7, #8b5cf6, #6366f1);
    background-size: 200% 100%;
    -webkit-background-clip: text;
    background-clip: text;
    -webkit-text-fill-color: transparent;
    animation: gradient-shift 2s linear infinite;
  }

  @keyframes gradient-shift {
    0% {
      background-position: 0% 50%;
    }
    100% {
      background-position: 200% 50%;
    }
  }
</style>
