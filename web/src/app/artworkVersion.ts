import { useSyncExternalStore } from "react";

// A picked image keeps its URL, and the browser keeps the old bytes for an hour: the version is the cache buster.
const versions = new Map<string, number>();
const listeners = new Set<() => void>();

export function bumpArtwork(subject: "works" | "people", id: number) {
  const key = `${subject}-${id}`;
  versions.set(key, (versions.get(key) ?? 0) + 1);
  listeners.forEach((listener) => listener());
}

export function useArtworkVersion(subject: "works" | "people", id: number): number {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => versions.get(`${subject}-${id}`) ?? 0,
  );
}
