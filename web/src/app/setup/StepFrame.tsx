import type { ReactNode } from "react";

import { t } from "@/lib/i18n";

import { Panel, primaryButtonStyle, quietButtonStyle } from "./ui";

/** One step's panel with Back, Skip and Next always in the same place at the foot. */
export function StepFrame({
  title,
  description,
  children,
  onBack,
  onNext,
  onSkip,
  canNext = true,
  nextLabel,
  busy = false,
}: {
  title: string;
  description: string;
  children: ReactNode;
  onBack?: () => void;
  onNext: () => void;
  onSkip?: () => void;
  canNext?: boolean;
  nextLabel?: string;
  busy?: boolean;
}) {
  return (
    <Panel title={title} description={description}>
      {children}
      <div className="flex items-center gap-2 border-t border-border-subtle pt-3">
        <button type="button" onClick={onBack} disabled={!onBack} className={quietButtonStyle}>
          {t("setup.back")}
        </button>
        <span className="flex-1" />
        {onSkip && (
          <button type="button" onClick={onSkip} className={quietButtonStyle}>
            {t("setup.skip")}
          </button>
        )}
        <button type="button" onClick={onNext} disabled={!canNext || busy} className={primaryButtonStyle}>
          {nextLabel ?? t("setup.next")}
        </button>
      </div>
    </Panel>
  );
}
