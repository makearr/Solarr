import { useMediaTypes } from "@/lib/useMediaTypes";
import { t } from "@/lib/i18n";

import { FolderField, LibraryFields } from "./LibraryFields";
import { StepFrame } from "./StepFrame";
import { quietButtonStyle } from "./ui";
import { replaceLibrary, type WizardLibrary, type WizardState } from "./wizardState";

/** The fresh path's libraries: what each holds and the folder it lives in, as Solarr's container sees it. */
export function LibrariesStep({ state, update, onBack, onNext }: { state: WizardState; update: (next: WizardState) => void; onBack: () => void; onNext: () => void }) {
  const mediaTypes = useMediaTypes();
  const libraries = state.libraries;
  const add = () => {
    const unused = mediaTypes.find((type) => !libraries.some((library) => library.mediaType === type.key)) ?? mediaTypes[0];
    if (!unused) return;
    const fresh: WizardLibrary = { key: `lib-${Date.now()}`, name: unused.label, mediaType: unused.key, kind: "", folders: [""], starter: "balanced" };
    update({ ...state, libraries: [...libraries, fresh] });
  };
  const complete = libraries.every((library) => library.name.trim() !== "" && library.folders.some((folder) => folder.trim().startsWith("/")));
  return (
    <StepFrame
      title={t("setup.libraries.title")}
      description={t("setup.libraries.description")}
      onBack={onBack}
      onNext={onNext}
      onSkip={libraries.length === 0 ? onNext : undefined}
      canNext={libraries.length > 0 && complete}
    >
      <p className="rounded-md bg-canvas p-2 text-xs text-ink-muted">{t("setup.libraries.example")}</p>
      {libraries.map((library) => {
        const change = (patch: Partial<WizardLibrary>) => update(replaceLibrary(state, library.key, patch));
        return (
          <section key={library.key} className="space-y-3 rounded-md border border-border-subtle p-3">
            <LibraryFields library={library} onChange={change} />
            {library.folders.map((folder, index) => (
              <FolderField
                key={index}
                value={folder}
                label={t("setup.libraries.folder")}
                onChange={(path) => change({ folders: library.folders.map((one, at) => (at === index ? path : one)) })}
                onRemove={library.folders.length > 1 ? () => change({ folders: library.folders.filter((_, at) => at !== index) }) : undefined}
              />
            ))}
            <div className="flex gap-2">
              <button type="button" onClick={() => change({ folders: [...library.folders, ""] })} className={quietButtonStyle}>
                {t("setup.libraries.addFolder")}
              </button>
              <button type="button" onClick={() => update({ ...state, libraries: libraries.filter((one) => one.key !== library.key) })} className={quietButtonStyle}>
                {t("setup.libraries.remove")}
              </button>
            </div>
          </section>
        );
      })}
      <button type="button" onClick={add} className={quietButtonStyle}>
        {libraries.length === 0 ? t("setup.libraries.addFirst") : t("setup.libraries.addAnother")}
      </button>
    </StepFrame>
  );
}
