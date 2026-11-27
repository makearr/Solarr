import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { languageName } from "@/lib/languages";
import { t } from "@/lib/i18n";

import { LanguagePicker } from "../LanguagePicker";
import { StepFrame } from "./StepFrame";
import { quietButtonStyle } from "./ui";
import { suggestedLanguages, type WizardState } from "./wizardState";

// One click each; the editor below stays for anything else.
const PRESETS: readonly (readonly string[])[] = [["en"], ["en", "de"], ["de", "en"], ["fr", "en"], ["es", "en"], ["ja", "en"]];

function presetLabel(codes: readonly string[]): string {
  return codes.length === 1 ? t("setup.languages.only", { language: languageName(codes[0]!) }) : codes.map(languageName).join(" + ");
}

/** The audio languages every library starts with; the choice is saved when the assistant finishes. */
export function LanguagesStep({ state, onChange, onBack, onNext }: {
  state: WizardState;
  onChange: (languages: string[]) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  const current = useQuery({ queryKey: ["defaultLanguages"], queryFn: () => ok(api.GET("/defaultlanguages")) });
  const suggestion = suggestedLanguages(state);
  const [languages, setLanguages] = useState<string[]>(state.languages ?? suggestion);
  useEffect(() => {
    if (languages.length === 0 && current.data) setLanguages(current.data.languages.length > 0 ? current.data.languages : ["en"]);
  }, [current.data, languages.length]);
  const choose = (next: string[]) => {
    setLanguages(next);
    onChange(next);
  };
  return (
    <StepFrame
      title={t("setup.languages.title")}
      description={t("setup.languages.description")}
      onBack={onBack}
      onNext={() => {
        onChange(languages);
        onNext();
      }}
      canNext={languages.length > 0}
    >
      {suggestion.length > 0 && JSON.stringify(suggestion) !== JSON.stringify(languages) && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-border-subtle bg-canvas p-3 text-xs">
          <span className="flex-1">{t("setup.languages.fromArr", { languages: suggestion.map(languageName).join(" + ") })}</span>
          <button type="button" onClick={() => choose(suggestion)} className={quietButtonStyle}>
            {t("setup.languages.useThem")}
          </button>
        </div>
      )}
      <div className="flex flex-wrap gap-2" role="group" aria-label={t("setup.languages.presets")}>
        {PRESETS.map((codes) => {
          const active = JSON.stringify(codes) === JSON.stringify(languages);
          return (
            <button
              key={codes.join("-")}
              type="button"
              aria-pressed={active}
              onClick={() => choose([...codes])}
              className={`rounded-full border px-3 py-1 text-xs ${active ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"}`}
            >
              {presetLabel(codes)}
            </button>
          );
        })}
      </div>
      <LanguagePicker label={t("setup.languages.label")} help={t("setup.languages.help")} value={languages} onChange={choose} />
    </StepFrame>
  );
}
