import type { ReactNode } from "react";

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
          <h2 className="text-sm font-medium">{title}</h2>
          <p className="max-w-xl text-xs text-ink-muted">{help}</p>
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
  disabled: boolean;
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

/** The save bar of a settings page: stays in view at the foot of the scrolling pane. */
export function SaveBar({
  dirty,
  busy,
  onSave,
  onUndo,
  error,
}: {
  dirty: boolean;
  busy: boolean;
  onSave: () => void;
  onUndo?: () => void;
  error?: string | null;
}) {
  return (
    <div className="sticky bottom-0 -mx-1 flex items-center gap-3 border-t border-border-subtle bg-canvas/95 px-1 py-3 backdrop-blur">
      <button
        type="button"
        disabled={busy || !dirty}
        onClick={onSave}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {busy ? "Saving…" : "Save"}
      </button>
      {dirty && onUndo && (
        <button type="button" onClick={onUndo} className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink">
          Undo
        </button>
      )}
      {dirty && <span className="text-xs text-ink-muted">Unsaved changes</span>}
      {error && <span className="text-xs text-danger">{error}</span>}
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
