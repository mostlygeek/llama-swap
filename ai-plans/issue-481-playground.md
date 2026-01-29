# Playground Page Implementation Plan

## Overview
Create a new Playground page in ui-svelte with tabbed interface for Chat, Images, and Audio. Chat tab will be fully functional with model selection, streaming responses, and message management. Images and Audio tabs show placeholder messages.

## Files to Create

### 1. `/Volumes/devdisk/llama-swap/ui-svelte/src/routes/Playground.svelte`
Main page component with tab navigation:
- State: `selectedTab` (chat/images/audio) using persistent store
- Top buttons for tab selection
- Conditional rendering based on active tab
- Use `.card` class for consistent styling

### 2. `/Volumes/devdisk/llama-swap/ui-svelte/src/lib/chatApi.ts`
Streaming chat API utilities:
- `streamChatCompletion()` async generator function
- Parses SSE format: `data: {...}` lines
- Supports AbortSignal for cancellation
- Yields content chunks incrementally
- Handles `[DONE]` signal and errors

### 3. `/Volumes/devdisk/llama-swap/ui-svelte/src/components/playground/ChatInterface.svelte`
Main chat implementation:
- **State:**
  - `messages`: ChatMessage[] (user and assistant messages)
  - `selectedModel`: string (persistent store)
  - `isStreaming`: boolean
  - `userInput`: string
  - `abortController`: AbortController | null
- **Layout:** Model selector → Message area → Input controls
- **Features:**
  - Send message (adds to history, streams response)
  - Cancel streaming (abort controller)
  - New chat (clear messages, cancel if streaming)
  - Auto-scroll to bottom
  - Disable inputs during streaming

### 4. `/Volumes/devdisk/llama-swap/ui-svelte/src/components/playground/ChatMessage.svelte`
Individual message display:
- Props: `role` (user/assistant), `content`, `isStreaming`
- Different styling for user vs assistant messages
- **Markdown rendering** using `marked` library for assistant messages
- Code block syntax highlighting with `highlight.js`
- User messages rendered as plain text (no markdown)
- Optional streaming cursor for assistant messages

### 5. `/Volumes/devdisk/llama-swap/ui-svelte/src/lib/markdown.ts`
Markdown rendering utilities:
- Configure `marked` with safe defaults (no HTML passthrough)
- Integrate `highlight.js` for code syntax highlighting
- Export `renderMarkdown(content: string): string` function
- Handle edge cases (empty content, malformed markdown)

### 6. `/Volumes/devdisk/llama-swap/ui-svelte/src/components/playground/PlaceholderTab.svelte`
Reusable placeholder component:
- Props: `featureName` (string)
- Centered "To be implemented" message
- Simple card-based layout

## Files to Modify

### 1. `/Volumes/devdisk/llama-swap/ui-svelte/src/App.svelte`
- Import Playground component
- Add route: `"/playground": Playground` to routes object (around line 11-16)

### 2. `/Volumes/devdisk/llama-swap/ui-svelte/src/components/Header.svelte`
- Add navigation link between Logs and theme toggle (after line 70):
```svelte
<a
  href="/playground"
  use:link
  class="text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1"
  class:font-semibold={isActive("/playground", $location)}
>
  Playground
</a>
```

### 3. `/Volumes/devdisk/llama-swap/ui-svelte/src/lib/types.ts`
Add chat-related types (append to file):
```typescript
export interface ChatMessage {
  role: "user" | "assistant" | "system";
  content: string;
}

export interface ChatCompletionRequest {
  model: string;
  messages: ChatMessage[];
  stream: boolean;
  temperature?: number;
  max_tokens?: number;
}
```

## Implementation Details

### API Integration
- **Endpoint:** POST `/v1/chat/completions` (OpenAI-compatible, proxied to llama.cpp)
- **Request format:** `{model, messages, stream: true}`
- **Response format:** SSE with `data: {...}` lines
- **Parsing:** Extract `choices[0].delta.content` from each chunk
- **Completion:** `data: [DONE]` signals end of stream

### State Management
- **Persistent:** Selected model saved to localStorage (`playground-selected-model`)
- **Component-level:** Messages, input, streaming status use Svelte 5 `$state` rune
- **Reactive:** Auto-scroll on new messages, disable controls during streaming

### Streaming Cancellation
1. Create AbortController before fetch
2. Pass `signal` to fetch options
3. On cancel button click: `abortController.abort()`
4. Catch AbortError in try/catch
5. Clean up: set `isStreaming = false`, `abortController = null`

### Model Selector
- Filter models: `$models.filter(m => m.state === 'ready')`
- Show only loaded models in dropdown
- Disable selector during streaming
- Handle empty state (no ready models)

### Message Flow
1. User enters message → Add to messages array as user message
2. Add empty assistant message to array
3. Start streaming, update assistant message incrementally
4. On chunk received: append to last message content
5. On completion or cancel: mark streaming complete

### Auto-scroll Behavior
- Bind message container: `bind:this={messagesContainer}`
- On new content: `messagesContainer?.scrollTo({top: messagesContainer.scrollHeight, behavior: 'smooth'})`
- Use `$effect()` to watch messages array

### UI Patterns
- **Tab buttons:** Active tab uses `bg-primary text-btn-primary-text`
- **Model selector:** Full-width dropdown, disabled when streaming
- **Send button:** Disabled when no input or no model selected
- **Cancel button:** Only visible during streaming
- **New Chat button:** Always available, confirms if conversation exists

### Markdown Rendering
- **Dependencies:** `marked` for markdown parsing, `highlight.js` for code highlighting
- **Security:** Disable HTML in markdown to prevent XSS
- **Styling:** Use Tailwind prose classes or custom styles for rendered markdown
- **Code blocks:** Apply syntax highlighting, add copy button (optional)
- **Streaming:** Re-render markdown on each content update during streaming

## Critical Files
- `src/routes/Playground.svelte` - Tab navigation and layout
- `src/lib/chatApi.ts` - Core streaming logic
- `src/lib/markdown.ts` - Markdown rendering with syntax highlighting
- `src/components/playground/ChatInterface.svelte` - Chat state and interactions
- `src/stores/api.ts` - Reference for fetch patterns
- `src/lib/types.ts` - Type definitions

## Verification Steps
1. Navigate to `/playground` - page loads with three tabs
2. Click Images tab → "To be implemented" message shown
3. Click Audio tab → "To be implemented" message shown
4. Click Chat tab → model selector and input area visible
5. Select a ready model → Send button enabled
6. Type message and send → message appears, streaming response starts
7. During streaming → Cancel button visible, can abort
8. Click Cancel → streaming stops, partial response shown
9. Click New Chat → messages cleared
10. Reload page → selected model persists
11. Test on narrow screen → layout adapts
12. No models ready → appropriate message shown
13. Ask model to write code → code blocks render with syntax highlighting
14. Ask model for markdown list → renders as proper list with bullets
