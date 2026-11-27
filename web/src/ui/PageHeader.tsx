import { ChevronRight } from "lucide-react";
import { Fragment, type ReactNode } from "react";

import { t } from "@/lib/i18n";

/** The head of every page: breadcrumbs on a deep page, image left, title and subtitle, facts below, the primary action on the
 *  right, and the page's tab row underneath. */
export function PageHeader({
  breadcrumbs,
  tabs,
  image,
  title,
  subtitle,
  facts,
  actions,
}: {
  /** The trail above the title, outermost first; each a link except the last, which is the page itself. */
  breadcrumbs?: readonly ReactNode[];
  tabs?: ReactNode;
  image?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  facts?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-2">
      {breadcrumbs && breadcrumbs.length > 1 && (
        <nav aria-label={t("pageHeader.breadcrumbs")} className="flex flex-wrap items-center gap-1 text-xs text-ink-muted">
          {breadcrumbs.map((crumb, index) => (
            <Fragment key={index}>
              {index > 0 && <ChevronRight aria-hidden className="size-3 shrink-0" />}
              {crumb}
            </Fragment>
          ))}
        </nav>
      )}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start">
        {image && <div className="w-40 shrink-0">{image}</div>}
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
            <div className="min-w-0 flex-1">
              <h1 className="text-xl font-semibold">{title}</h1>
              {subtitle && <div className="text-sm text-ink-muted">{subtitle}</div>}
            </div>
            {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
          </div>
          {facts}
        </div>
      </div>
      {tabs}
    </header>
  );
}
