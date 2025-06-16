import { useState, useEffect, useCallback } from "react";

export function usePersistentState<T>(key: string, initialValue: T): [T, (value: T | ((prevState: T) => T)) => void] {
  const [state, setState] = useState<T>(() => {
    if (typeof window === "undefined") return initialValue;
    try {
      const saved = localStorage.getItem(key);
      return saved !== null ? JSON.parse(saved) : initialValue;
    } catch (e) {
      console.error(`Error parsing stored value for ${key}`, e);
      return initialValue;
    }
  });

  const setPersistentState = useCallback(
    (value: T | ((prevState: T) => T)) => {
      setState((prev) => {
        const nextValue = typeof value === "function" ? (value as (prevState: T) => T)(prev) : value;
        try {
          localStorage.setItem(key, JSON.stringify(nextValue));
        } catch (e) {
          console.error(`Error saving value for ${key}`, e);
        }
        return nextValue;
      });
    },
    [key]
  );

  useEffect(() => {
    try {
      localStorage.setItem(key, JSON.stringify(state));
    } catch (e) {
      console.error(`Error saving value for ${key}`, e);
    }
  }, [key, state]);

  return [state, setPersistentState];
}
