import { writable, type Writable } from "svelte/store";

export function persistentStore<T>(key: string, initialValue: T): Writable<T> {
  // Get initial value from localStorage or use default
  let storedValue = initialValue;
  if (typeof window !== "undefined") {
    try {
      const saved = localStorage.getItem(key);
      if (saved !== null) {
        storedValue = JSON.parse(saved);
      }
    } catch (e) {
      console.error(`Error parsing stored value for ${key}`, e);
    }
  }

  const store = writable<T>(storedValue);

  // Subscribe to changes and save to localStorage
  store.subscribe((value) => {
    if (typeof window !== "undefined") {
      try {
        localStorage.setItem(key, JSON.stringify(value));
      } catch (e) {
        console.error(`Error saving value for ${key}`, e);
      }
    }
  });

  return store;
}
