// Clipboard helper that works in both secure (https) and non-secure (http) contexts.

/**
 * Copy text to the clipboard. Uses the async Clipboard API when available and
 * falls back to a hidden textarea + execCommand for non-secure contexts.
 * Returns true on success, false on failure.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
    } else {
      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.style.position = "fixed";
      textarea.style.left = "-9999px";
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand("copy");
      document.body.removeChild(textarea);
    }
    return true;
  } catch (err) {
    console.error("Failed to copy:", err);
    return false;
  }
}
