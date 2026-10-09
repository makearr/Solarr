import type { ReactNode } from "react";

/** What a page or section shows with nothing in it: what that means, and what to do about it. */
export function EmptyState({ icon, title, hint, action }: { icon?: ReactNode; title: ReactNode; hint?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border-subtle px-6 py-10 text-center">
      {icon && <span className="text-ink-muted">{icon}</span>}
      <p className="text-sm font-medium">{title}</p>
      {hint && <p className="max-w-md text-xs text-ink-muted">{hint}</p>}
      {action}
    </div>
  );
}
