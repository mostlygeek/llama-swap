// Hash-based router replacing svelte-spa-router. Mirrors App.svelte: the Playground
// page is always mounted and toggled visible on "/", while the other routes mount
// into a second container (with unmount() called on the prior page so timers/charts
// are cleaned up).
import { observable } from "./store.js";

export const currentRoute = observable("/");

function normalize(hash) {
  // svelte-spa-router uses "#/path"; default to "/"
  let h = hash.replace(/^#/, "");
  if (!h.startsWith("/")) h = "/" + h;
  return h;
}

// routes: { "/path": factory } where factory() -> { el, destroy? }
// playgroundFactory is mounted once and toggled.
export function startRouter({ routes, playgroundFactory, playgroundContainer, routeContainer }) {
  const known = new Set(Object.keys(routes));

  const playground = playgroundFactory();
  playgroundContainer.appendChild(playground.el);

  let active = null; // { instance, path }

  function resolve(path) {
    if (path === "/") return null; // playground
    if (routes[path]) return path;
    // startsWith match for nested paths (e.g. /models/foo)
    for (const p of known) {
      if (p !== "/" && path.startsWith(p)) return p;
    }
    return "/"; // wildcard -> playground
  }

  function render() {
    const path = normalize(location.hash || "#/");
    const matched = resolve(path);
    currentRoute.set(matched === null ? "/" : matched);

    const isPlayground = matched === null || matched === "/";
    playgroundContainer.style.display = isPlayground ? "" : "none";
    routeContainer.style.display = isPlayground ? "none" : "";

    if (isPlayground) {
      teardownActive();
      return;
    }

    if (active && active.path === matched) return; // already mounted
    teardownActive();
    const instance = routes[matched]();
    routeContainer.appendChild(instance.el);
    active = { instance, path: matched };
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
