import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";

import { StepFrame } from "./StepFrame";
import { appTitle } from "./ConnectStep";
import { replaceLibrary, type Starter, type WizardState } from "./wizardState";

type QualityProfile = components["schemas"]["QualityProfile"];

// The shipped profile each starter means, by library type; a type with no entry keeps what its library starts with.
const STARTERS: Record<string, Record<"best" | "balanced" | "space", string>> = {
  music: { best: "Lossless preferred", balanced: "Lossy 320", space: "Space conscious" },
  tv: { best: "UHD 2160p", balanced: "WEB 1080p", space: "Space conscious (x265 1080p)" },
  movie: { best: "Movie UHD 2160p", balanced: "Movie HD 1080p", space: "Movie space conscious (x265 1080p)" },
  book: { best: "Ebook: EPUB preferred", balanced: "Ebook: EPUB preferred", space: "Ebook: any format" },
  audiobook: { best: "Audiobook: M4B preferred", balanced: "Audiobook: M4B preferred", space: "Audiobook: any format" },
  adult: { best: "Adult: 1080p preferred", balanced: "Adult: 720p to 1080p", space: "Adult: any quality" },
  comic: { best: "High resolution", balanced: "CBZ preferred", space: "Any comic" },
};

/** The quality profile a starter stands for in a library of this type, when there is one. */
export function starterProfile(profiles: readonly QualityProfile[], mediaType: string, starter: Starter): QualityProfile | undefined {
  if (starter === "arr" || starter === "keep") return undefined;
  const name = STARTERS[mediaType]?.[starter];
  return profiles.find((profile) => profile.name === name);
}

/** One starter quality per library, or the *Arr's own mapped over; each is a profile Solarr already has. */
export function QualityStep({ state, update, onBack, onNext }: { state: WizardState; update: (next: WizardState) => void; onBack: () => void; onNext: () => void }) {
  const profiles = useQuery({ queryKey: ["qualityprofiles"], queryFn: () => ok(api.GET("/qualityprofiles")) });
  const choices: { starter: Starter; label: string; help: string }[] = [
    { starter: "best", label: t("setup.quality.best"), help: t("setup.quality.bestHelp") },
    { starter: "balanced", label: t("setup.quality.balanced"), help: t("setup.quality.balancedHelp") },
    { starter: "space", label: t("setup.quality.space"), help: t("setup.quality.spaceHelp") },
  ];
  return (
    <StepFrame title={t("setup.quality.title")} description={t("setup.quality.description")} onBack={onBack} onNext={onNext} onSkip={onNext}>
      {state.libraries.length === 0 && <p className="text-xs text-ink-muted">{t("setup.quality.none")}</p>}
      {state.libraries.map((library) => {
        const connection = state.connections.find((one) => one.libraryKey === library.key);
        const reused = library.libraryId !== undefined;
        const options = [
          ...(connection ? [{ starter: "arr" as Starter, label: t("setup.quality.arr", { app: appTitle(connection.app) }), help: t("setup.quality.arrHelp") }] : []),
          ...choices.filter((choice) => starterProfile(profiles.data ?? [], library.mediaType, choice.starter) !== undefined),
          { starter: "keep" as Starter, label: t("setup.quality.keep"), help: t("setup.quality.keepHelp") },
        ];
        return (
          <fieldset key={library.key} className="space-y-2 rounded-md border border-border-subtle p-3">
            <legend className="px-1 text-sm font-medium">{library.name}</legend>
            {reused && <p className="text-xs text-ink-muted">{t("setup.quality.existing")}</p>}
            {options.map((option) => {
              const profile = starterProfile(profiles.data ?? [], library.mediaType, option.starter);
              const picked = reused ? option.starter === "keep" : library.starter === option.starter;
              return (
                <label key={option.starter} className="flex items-start gap-2 text-sm">
                  <input
                    type="radio"
                    name={`starter-${library.key}`}
                    checked={picked}
                    disabled={reused}
                    onChange={() => update(replaceLibrary(state, library.key, { starter: option.starter }))}
                  />
                  <span>
                    {option.label}
                    <span className="block text-xs text-ink-muted">
                      {option.help}
                      {profile ? ` (${profile.name})` : ""}
                    </span>
                  </span>
                </label>
              );
            })}
          </fieldset>
        );
      })}
    </StepFrame>
  );
}
