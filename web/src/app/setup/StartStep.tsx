import { t } from "@/lib/i18n";

import type { WizardPath } from "./wizardState";
import { Panel, quietButtonStyle } from "./ui";

/** The first question: is there an *Arr to move from, or is this a fresh start. */
export function StartStep({ onChoose, onLeave, rerun }: { onChoose: (path: WizardPath) => void; onLeave: () => void; rerun: boolean }) {
  const card = "flex flex-col gap-1 rounded-md border border-border-subtle bg-canvas p-4 text-left hover:border-accent focus-visible:outline-2 focus-visible:outline-accent";
  return (
    <Panel title={t("setup.start.title")} description={rerun ? t("setup.start.descriptionRerun") : t("setup.start.description")}>
      <div className="grid gap-3 sm:grid-cols-2">
        <button type="button" className={card} onClick={() => onChoose("migrate")}>
          <span className="text-sm font-medium">{t("setup.start.migrate")}</span>
          <span className="text-xs text-ink-muted">{t("setup.start.migrateHelp")}</span>
        </button>
        <button type="button" className={card} onClick={() => onChoose("fresh")}>
          <span className="text-sm font-medium">{t("setup.start.fresh")}</span>
          <span className="text-xs text-ink-muted">{t("setup.start.freshHelp")}</span>
        </button>
      </div>
      <button type="button" onClick={onLeave} className={quietButtonStyle}>
        {rerun ? t("setup.start.close") : t("setup.start.skipAll")}
      </button>
    </Panel>
  );
}
