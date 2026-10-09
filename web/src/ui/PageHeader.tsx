import type { ReactNode } from "react";

/** The head of an entity page: image left, title and subtitle, facts below, the action row on the right. */
export function PageHeader({
  image,
  title,
  subtitle,
  facts,
  actions,
}: {
  image?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  facts?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-4 sm:flex-row sm:items-start">
      {image && <div className="w-40 shrink-0">{image}</div>}
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
          <div className="min-w-0 flex-1">
            <h1 className="text-xl font-semibold">{title}</h1>
            {subtitle && <div className="text-sm text-ink-muted">{subtitle}</div>}
          </div>
          {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
        </div>
        {facts}
      </div>
    </header>
  );
}
