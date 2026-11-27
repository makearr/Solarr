import type { ReactNode } from "react";

import { t } from "@/lib/i18n";

export const inputStyle =
  "w-full rounded-md border border-border-subtle bg-canvas px-2.5 py-1.5 text-sm " +
  "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent";

export const primaryButtonStyle =
  "rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink " +
  "disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

export const quietButtonStyle =
  "rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted " +
  "hover:text-ink disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

/** A server answer's own `detail`, written to be read by a person; a thrown non-answer gets the fallback. */
export function detailOf(error: unknown, fallback: string): string {
  return typeof error === "object" && error !== null && "detail" in error ? String((error as { detail: unknown }).detail) : fallback;
}

export function Problem({ error }: { error: unknown }) {
  return (
    <p role="alert" className="rounded-md border border-danger/40 bg-danger/10 p-2 text-sm text-danger">
      {detailOf(error, t("setup.rejected"))}
    </p>
  );
}

export function Panel({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <section className="space-y-4 rounded-lg border border-border-subtle bg-surface p-5">
      <div className="space-y-1">
        <h2 className="text-sm font-medium">{title}</h2>
        <p className="text-xs text-ink-muted">{description}</p>
      </div>
      {children}
    </section>
  );
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-ink-muted">{label}</span>
      {children}
      {hint && <span className="block text-xs text-ink-muted">{hint}</span>}
    </label>
  );
}

export function Centered({ children }: { children: ReactNode }) {
  return <div className="flex min-h-full items-center justify-center p-6">{children}</div>;
}

/** A status line with a mark: ok green, otherwise the warning colour. */
export function Verdict({ ok, children }: { ok: boolean; children: ReactNode }) {
  return (
    <p role="status" className={`text-xs ${ok ? "text-ok" : "text-warn"}`}>
      <span aria-hidden>{ok ? "✓ " : "⚠ "}</span>
      {children}
    </p>
  );
}
