import { t } from "@/lib/i18n";

import { StepFrame } from "./StepFrame";
import { FEATURES, type Feature, type WizardState } from "./wizardState";

/** The optional features, all off until chosen here; each says what it does, what it costs in speed and what can go wrong. */
export function FeaturesStep({ state, update, onBack, onNext }: { state: WizardState; update: (next: WizardState) => void; onBack: () => void; onNext: () => void }) {
  const chosen = state.features ?? [];
  const toggle = (feature: Feature, on: boolean) => update({ ...state, features: on ? [...chosen, feature] : chosen.filter((one) => one !== feature) });
  return (
    <StepFrame title={t("setup.features.title")} description={t("setup.features.description")} onBack={onBack} onNext={onNext} onSkip={onNext}>
      <div className="space-y-3">
        {FEATURES.map((feature) => (
          <label key={feature} className="flex items-start gap-2 rounded-md border border-border-subtle p-3 text-sm">
            <input type="checkbox" className="mt-1" checked={chosen.includes(feature)} onChange={(event) => toggle(feature, event.target.checked)} />
            <span className="space-y-1">
              <span className="block font-medium">{t(`setup.feature.${feature}`)}</span>
              <span className="block text-xs text-ink-muted">{t(`setup.feature.${feature}.what`)}</span>
              <span className="block text-xs text-ink-muted">{t(`setup.feature.${feature}.speed`)}</span>
              <span className="block text-xs text-ink-muted">{t(`setup.feature.${feature}.risk`)}</span>
            </span>
          </label>
        ))}
      </div>
    </StepFrame>
  );
}
