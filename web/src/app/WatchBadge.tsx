import { Check, Eye } from "lucide-react";

import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";

type Watch = components["schemas"]["WorkWatch"];

/** What a tile or row shows of watch state: a check once played, an eye while it is partly played. */
export function WatchBadge({ watch }: { watch: Watch | undefined }) {
  if (!watch) return null;
  const label =
    watch.status === "yes"
      ? watch.watched_by != null && watch.of != null
        ? t("watch.watchedBy", { count: watch.watched_by, of: watch.of })
        : t("watch.watched")
      : t("watch.inProgress");
  const Icon = watch.status === "yes" ? Check : Eye;
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={`flex size-6 items-center justify-center rounded-full shadow-sm ${watch.status === "yes" ? "bg-ok text-accent-ink" : "bg-canvas/90 text-ink"}`}
    >
      <Icon aria-hidden className="size-3.5" />
    </span>
  );
}
