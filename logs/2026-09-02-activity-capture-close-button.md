# 2026-09-02 — Fix dead close buttons in the activity capture dialog

## Symptom / goal

On `/activity`, clicking "View" on an entry opens the capture inspector modal,
but neither close control worked — the header `×` nor the footer `Close`
button dismissed the dialog. The only escapes were the Escape key or a
backdrop click.

## Diagnosis

`internal/server/ui_dist/js/components/captureDialog.js`'s `render()` emits two
`[data-close]` buttons (header `×` at ~line 189, footer `Close` at ~line 210),
but wired the click handler with:

```js
dlg.querySelector("[data-close]")?.addEventListener("click", close);
```

`querySelector` returns only the first match, so at most one button received
the handler. In the main capture view the header `×` was bound and the footer
`Close` was dead; the ordering/branch determined which button worked, and the
mismatch is exactly what the user hit.

## Change

`internal/server/ui_dist/js/components/captureDialog.js` — bind every
`[data-close]` button instead of just the first:

```diff
-    dlg.querySelector("[data-close]")?.addEventListener("click", close);
+    dlg.querySelectorAll("[data-close]").forEach((btn) => btn.addEventListener("click", close));
```

## Commands

- `aidc-scan` → semgrep + gitleaks clean; language scanners skipped (only a
  hand-authored JS file under `internal/server/ui_dist/` changed, no build
  step, no Go/dependency changes).

## Verification

Reviewed the rendered markup: both `[data-close]` buttons in every render
branch now match `querySelectorAll` and receive the `close` handler.

## Notes

The web UI under `internal/server/ui_dist/` is vanilla ES-module JS served
directly with no build step, so no rebuild was needed. Go serving/embedding is
covered by `internal/server/ui_test.go`, which is unaffected.
