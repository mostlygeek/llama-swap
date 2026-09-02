// Hash-based router replacing svelte-spa-router. Mirrors App.svelte: the Playground
// page is always mounted and toggled visible on "/", while the other routes mount
// into a second container (with unmount() called on the prior page so timers/charts
// are cleaned up).
import { observable } from "./store.js";

// The matched route pattern (e.g. "/models/:id"), used for nav highlighting.
export const currentRoute = observable("/");
// The actual location path (e.g. "/models/qwen"), used to extract params.
export const currentPath = observable("/");

function normalize(hash) {
  // svelte-spa-router uses "#/path"; default to "/"
  let h = hash.replace(/^#/, "");
  if (!h.startsWith("/")) h = "/" + h;
  return h;
}

// Does a route pattern match a concrete path? Segments starting with ":" match
// any non-empty segment (e.g. "/models/:id" matches "/models/qwen").
function patternMatches(pattern, path) {
  if (pattern === path) return true;
  const pSegs = pattern.split("/").filter(Boolean);
  const segs = path.split("/").filter(Boolean);
  if (pSegs.length !== segs.length) return false;
  return pSegs.every((p, i) => (p.startsWith(":") ? segs[i] !== "" : p === segs[i]));
}

// routes: { "/path": factory } where factory() -> { el, destroy? }
// playgroundFactory is mounted once and toggled.
export function startRouter({ routes, playgroundFactory, playgroundContainer, routeContainer }) {
  const patterns = Object.keys(routes);

  const playground = playgroundFactory();
  playgroundContainer.appendChild(playground.el);

  let active = null; // { instance, path }

  function resolve(path) {
    if (path === "/") return null; // playground
    // Exact match first, then param patterns; more specific (longer literal
    // prefix) patterns win so "/models" is not shadowed by "/models/:id".
    if (routes[path]) return path;
    const candidates = patterns.filter((p) => p.includes("/:") && patternMatches(p, path));
    if (candidates.length > 0) {
      candidates.sort((a, b) => b.length - a.length);
      return candidates[0];
    }
    // startsWith match for nested paths (e.g. a legacy "/models/foo" link when
    // only "/models" exists)
    for (const p of patterns) {
      if (p !== "/" && !p.includes("/:") && path.startsWith(p)) return p;
    }
    return "/"; // wildcard -> playground
  }

  function render() {
    const path = normalize(location.hash || "#/");
    const matched = resolve(path);
    const routePattern = matched === null ? "/" : matched;
    currentRoute.set(routePattern);
    currentPath.set(path);

    const isPlayground = matched === null || matched === "/";
    playgroundContainer.style.display = isPlayground ? "" : "none";
    routeContainer.style.display = isPlayground ? "none" : "";

    if (isPlayground) {
      teardownActive();
      return;
    }

    // Remount when either the pattern or the concrete path changes, so
    // /models/a -> /models/b (same pattern, different param) rebuilds the page.
    if (active && active.path === path) return;
    teardownActive();
    const instance = routes[matched]();
    routeContainer.appendChild(instance.el);
    active = { instance, path };
  }

  function teardownActive() {
    if (!active) return;
    try {
      active.instance.destroy?.();
    } catch (e) {
      console.error("route destroy error", e);
    }
    if (active.instance.el && active.instance.el.parentNode) {
      active.instance.el.parentNode.removeChild(active.instance.el);
    }
    active = null;
  }

  window.addEventListener("hashchange", render);
  render();

  return () => {
    window.removeEventListener("hashchange", render);
    teardownActive();
    playground.destroy?.();
  };
}

// Navigate helper for in-app links.
export function navigate(path) {
  location.hash = path;
}
