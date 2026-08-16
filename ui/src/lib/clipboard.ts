// Clipboard helper that works in both secure (https) and non-secure (http) contexts.

/**
 * Copy text to the clipboard. Uses the async Clipboard API when available and
 * falls back to a hidden textarea + execCommand for non-secure contexts.
 * Returns true on success, false on failure.
 *
 * Pass `container` when copying from inside a modal dialog. The fallback has to
 * focus and select the textarea, and a dialog's focus trap yanks focus straight
 * back out of any element parked on document.body — which drops the selection
 * before execCommand runs, so nothing gets copied. Parking it inside the dialog
 * keeps it within the trap.
 */
export async function copyText(
  text: string,
  container: HTMLElement | null = null
): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }

    const host = container ?? document.body;
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const textarea = document.createElement("textarea");
    textarea.value = text;
    // Read-only keeps mobile keyboards from popping up; fixed + transparent
    // keeps the page from scrolling to it.
    textarea.readOnly = true;
    textarea.style.position = "fixed";
    textarea.style.top = "0";
    textarea.style.left = "0";
    textarea.style.opacity = "0";
    textarea.style.pointerEvents = "none";
    host.appendChild(textarea);
    textarea.focus();
    textarea.select();
    const copied = document.execCommand("copy");
    host.removeChild(textarea);
    previouslyFocused?.focus?.();
    return copied;
  } catch (err) {
    console.error("Failed to copy:", err);
    return false;
  }
}
