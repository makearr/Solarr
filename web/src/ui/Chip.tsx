import type { ReactNode } from "react";

export type ChipTone = "neutral" | "accent" | "ok" | "warn" | "danger" | "busy";

const TONES: Record<ChipTone, string> = {
  neutral: "border-border-subtle text-ink-muted",
  accent: "border-accent text-accent",
  ok: "border-ok text-ok",
  warn: "border-warn text-warn",
  danger: "border-danger text-danger",
  busy: "border-busy text-busy",
};

/** A small rounded label: a state, a format, a count. */
export function Chip({ tone = "neutral", title, children }: { tone?: ChipTone; title?: string; children: ReactNode }) {
  return (
    <span title={title} className={`inline-flex w-fit items-center gap-1 rounded-full border px-2 py-px text-xs ${TONES[tone]}`}>
      {children}
    </span>
  );
}
