import { Check } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { HelpTip } from "@/ui/HelpTip";

const AUTOSAVE_MS = 1000;

/** One group of related settings: a heading, what it does, an optional on/off switch and the fields. */
export function Card({
  title,
  help,
  toggle,
  tone = "plain",
  children,
}: {
  title: string;
  help: string;
  toggle?: ReactNode;
  /** "warn" borders a group whose current setting deserves a second look. */
  tone?: "plain" | "warn";
  children: ReactNode;
}) {
  return (
    <section className={`flex flex-col gap-3 rounded-lg border bg-surface p-4 ${tone === "warn" ? "border-warn" : "border-border-subtle"}`}>
      <div className="flex items-start gap-4">
        <div className="flex flex-1 flex-col gap-0.5">
          <h2 className="flex items-center gap-1.5 text-sm font-medium">
            {title}
            <HelpTip>{help}</HelpTip>
          </h2>
        </div>
        {toggle}
      </div>
      {children}
    </section>
  );
}

export function Toggle({
  label,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <label className="flex shrink-0 cursor-pointer items-center gap-2 text-xs text-ink">
      {label}
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="h-5 w-9 cursor-pointer appearance-none rounded-full bg-border-subtle transition-colors
                   checked:bg-accent relative after:absolute after:left-0.5 after:top-0.5 after:size-4
                   after:rounded-full after:bg-white after:transition-transform checked:after:translate-x-4"
      />
    </label>
  );
}

/** A labelled field: what it is, the control, and a line of help under it. */
export function Field({ label, help, children, className = "" }: { label: string; help?: string; children: ReactNode; className?: string }) {
  return (
    <label className={`flex flex-col gap-1 text-xs text-ink-muted ${className}`}>
      {label}
      {children}
      {help && <span>{help}</span>}
    </label>
  );
}

/** Saves a settings form by itself: a second after the last edit inside its form (an edit that fails stays unsaved until the next one),
 *  with the state beside it. There is no Save button; Undo returns to what is stored. */
export function SaveBar({
  dirty,
  busy,
  onSave,
  onUndo,
  error,
  canSave = true,
  sticky = true,
}: {
  dirty: boolean;
  busy: boolean;
  /** What saves; left out, the form the bar sits in is submitted. */
  onSave?: () => void;
  onUndo?: () => void;
  error?: string | null;
  /** False while the fields hold something that must not be stored yet; the change waits. */
  canSave?: boolean;
  /** Stays in view at the foot of a scrolling page; false sits where it is written, inside a card. */
  sticky?: boolean;
}) {
  const bar = useRef<HTMLDivElement>(null);
  const latest = useRef({ dirty: dirty && canSave, busy, onSave });
  latest.current = { dirty: dirty && canSave, busy, onSave };
  const [saved, setSaved] = useState(false);
  const wasBusy = useRef(false);

  useEffect(() => {
    if (wasBusy.current && !busy && !dirty && !error) setSaved(true);
    if (dirty) setSaved(false);
    wasBusy.current = busy;
  }, [busy, dirty, error]);

  useEffect(() => {
    const scope = bar.current?.closest("form") ?? bar.current?.closest("main") ?? bar.current?.parentElement;
    if (!scope) return;
    let timer: number | undefined;
    const attempt = () => {
      const now = latest.current;
      if (now.busy) timer = window.setTimeout(attempt, 500);
      else if (now.dirty) (now.onSave ?? (() => (scope instanceof HTMLFormElement ? scope.requestSubmit() : undefined)))();
    };
    const edited = (event: Event) => {
      if (bar.current?.contains(event.target as Node)) return;
      window.clearTimeout(timer);
      timer = window.setTimeout(attempt, AUTOSAVE_MS);
    };
    scope.addEventListener("input", edited);
    scope.addEventListener("change", edited);
    scope.addEventListener("click", edited);
    return () => {
      window.clearTimeout(timer);
      scope.removeEventListener("input", edited);
      scope.removeEventListener("change", edited);
      scope.removeEventListener("click", edited);
    };
  }, []);

  return (
    <div ref={bar} className={`flex min-h-8 items-center gap-3 text-xs text-ink-muted ${sticky ? "sticky bottom-0 -mx-1 min-h-12 border-t border-border-subtle bg-canvas/95 px-1 py-3 backdrop-blur" : ""}`} aria-live="polite">
      {busy && <span>Saving…</span>}
      {!busy && dirty && canSave && !error && <span>Unsaved changes: saving in a moment</span>}
      {!busy && !dirty && saved && !error && (
        <span className="flex items-center gap-1 text-ok">
          <Check aria-hidden className="size-3.5" />
          Saved
        </span>
      )}
      {dirty && onUndo && (
        <button type="button" onClick={onUndo} className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink">
          Undo
        </button>
      )}
      {error && <span className="text-danger">{error}</span>}
    </div>
  );
}

/** One setting as a row: its name and what it does on the left, the control on the right. Rows in a card are divided. */
export function SettingRow({ label, help, children, disabled }: { label: string; help?: string; children: ReactNode; disabled?: boolean }) {
  return (
    <label
      className={`flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-t border-border-subtle pt-3 first:border-t-0 first:pt-0 ${
        disabled ? "opacity-50" : ""
      }`}
    >
      <span className="flex min-w-48 flex-1 flex-col gap-0.5">
        <span className="text-sm text-ink">{label}</span>
        {help && <span className="max-w-xl text-xs text-ink-muted">{help}</span>}
      </span>
      <span className="flex shrink-0 items-center gap-2">{children}</span>
    </label>
  );
}

/** The on/off switch itself, for a SettingRow. */
export function Switch({ checked, disabled, onChange, label }: { checked: boolean; disabled?: boolean; onChange: (value: boolean) => void; label: string }) {
  return (
    <input
      type="checkbox"
      role="switch"
      aria-label={label}
      checked={checked}
      disabled={disabled}
      onChange={(event) => onChange(event.target.checked)}
      className="relative h-5 w-9 cursor-pointer appearance-none rounded-full bg-border-subtle transition-colors checked:bg-accent
                 after:absolute after:left-0.5 after:top-0.5 after:size-4 after:rounded-full after:bg-white after:transition-transform
                 checked:after:translate-x-4"
    />
  );
}
