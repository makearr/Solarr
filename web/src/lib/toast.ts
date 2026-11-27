import { useSyncExternalStore } from "react";

/** One message in the corner; `action` is the Undo (or Retry) the person may still take. */
export interface Toast {
  readonly id: number;
  readonly tone: "success" | "error" | "info";
  readonly message: string;
  readonly detail?: string;
  readonly action?: { readonly label: string; readonly run: () => void };
}

type Input = Omit<Toast, "id" | "tone"> & { tone?: Toast["tone"] };

const SHOWN_MS = { success: 6000, info: 6000, error: 12000 } as const;

let toasts: readonly Toast[] = [];
let nextId = 1;
const listeners = new Set<() => void>();

function publish(next: readonly Toast[]) {
  toasts = next;
  listeners.forEach((listener) => listener());
}

export function dismissToast(id: number) {
  publish(toasts.filter((toast) => toast.id !== id));
}

/** Shows a message and removes it after a while; returns its id. */
export function showToast(input: Input): number {
  const tone = input.tone ?? "success";
  // The same message again replaces the one showing, so a run of saved fields is one toast.
  const same = toasts.find((shown) => shown.message === input.message && shown.tone === tone && shown.action === undefined);
  const toast: Toast = { ...input, tone, id: nextId++ };
  publish([...toasts.filter((shown) => shown !== same), toast].slice(-4));
  window.setTimeout(() => dismissToast(toast.id), SHOWN_MS[toast.tone]);
  return toast.id;
}

export function useToasts(): readonly Toast[] {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => toasts,
  );
}
