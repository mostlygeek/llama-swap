// Helpers for telling an IME conversion-confirming Enter apart from an Enter
// that means "submit".
//
// Browsers deliver the Enter that confirms a Japanese/Chinese/Korean IME
// conversion as an ordinary keydown - same key, same target. There are only
// two signals that distinguish it:
//   - isComposing: the standard one, true for keydowns inside a composition
//     session.
//   - keyCode === 229: the legacy "composing" value engines still set. Kept as
//     a backstop for engines that don't set isComposing on the confirming key.

export function isComposingKey(event: Pick<KeyboardEvent, "isComposing" | "keyCode">): boolean {
  return event.isComposing || event.keyCode === 229;
}

// True when a keydown is a plain Enter meant to submit. Shift+Enter (newline)
// and IME conversion-confirm Enters are both false.
export function isSubmitEnter(
  event: Pick<KeyboardEvent, "key" | "shiftKey" | "isComposing" | "keyCode">,
): boolean {
  return event.key === "Enter" && !event.shiftKey && !isComposingKey(event);
}
