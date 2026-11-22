import { t } from "@/lib/i18n";
import { EmptyState } from "@/ui/EmptyState";

/** A per-library configuration path no screen answers, e.g. a stale bookmark. */
export function UnknownConfigSection() {
  return (
    <div className="p-6">
      <h1 className="sr-only">Not found</h1>
      <EmptyState title={t("unknownConfig.title")} />
    </div>
  );
}
