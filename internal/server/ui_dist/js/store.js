// Minimal observable store layer replacing Svelte's writable/derived/persistent.
// observable().subscribe(fn) calls fn immediately with the current value (matching
// Svelte's writable contract), then on every change, and returns an unsubscribe fn.

export function observable(initial) {
  let value = initial;
  const subs = new Set();
  return {
    get: () => value,
    set(v) {
      value = v;
      for (const fn of subs) fn(value);
    },
    update(fn) {
      value = fn(value);
      for (const f of subs) f(value);
    },
    subscribe(fn) {
      fn(value);
      subs.add(fn);
      return () => subs.delete(fn);
    },
  };
}

// derived([storeA, storeB], (a, b) => ...) — recomputes when any dependency changes.
export function derived(deps, fn) {
  const compute = () => fn(...deps.map((d) => d.get()));
  const out = observable(compute());
  for (const d of deps) {
    // skip the immediate fire on subscribe; recompute on subsequent changes
    let primed = false;
    d.subscribe(() => {
      if (!primed) {
        primed = true;
        return;
      }
      out.set(compute());
    });
  }
  return { get: out.get, subscribe: out.subscribe };
}

// persistent(key, initial) — observable backed by localStorage (JSON-serialized).
export function persistent(key, initial) {
  let start = initial;
  try {
    const saved = localStorage.getItem(key);
    if (saved !== null) start = JSON.parse(saved);
  } catch (e) {
    console.error("Error parsing stored value for", key, e);
  }
  const store = observable(start);
  store.subscribe((value) => {
    try {
      localStorage.setItem(key, JSON.stringify(value));
    } catch (e) {
      console.error("Error saving value for", key, e);
    }
  });
  return store;
}
