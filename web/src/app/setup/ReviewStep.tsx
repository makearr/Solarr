import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { languageName } from "@/lib/languages";
import { t } from "@/lib/i18n";

import { appTitle } from "./ConnectStep";
import { foldersOf, libraryIdOf, syncOptions } from "./runSetup";
import { StepFrame } from "./StepFrame";
import { Verdict } from "./ui";
import { NamingChanges } from "../ArrImportPreview";
import { replaceLibrary, type Starter, type WizardConnection, type WizardState } from "./wizardState";

function starterLabel(starter: Starter, app?: string): string {
  switch (starter) {
    case "arr":
      return t("setup.quality.arr", { app: appTitle(app ?? "") });
    case "best":
      return t("setup.quality.best");
    case "balanced":
      return t("setup.quality.balanced");
    case "space":
      return t("setup.quality.space");
    default:
      return t("setup.quality.keep");
  }
}

/** Everything that will happen, read before any of it does; the import figures come from the *Arr itself, read-only. */
export function ReviewStep({ state, update, onBack, onFinish }: { state: WizardState; update: (next: WizardState) => void; onBack: () => void; onFinish: () => void }) {
  const content = state.connections.filter((connection) => connection.libraryKey !== undefined);
  const prowlarr = state.connections.filter((connection) => connection.app === "prowlarr");
  const nothing = state.libraries.length === 0 && state.connections.length === 0 && !state.languages;
  return (
    <StepFrame title={t("setup.review.title")} description={t("setup.review.description")} onBack={onBack} onNext={onFinish} nextLabel={t("setup.review.finish")}>
      {nothing && <p className="text-xs text-ink-muted">{t("setup.review.nothing")}</p>}
      {state.languages && (
        <Section title={t("setup.review.languages")}>
          <p>{state.languages.map(languageName).join(", ")}</p>
        </Section>
      )}
      {state.libraries.map((library) => {
        const connection = state.connections.find((one) => one.libraryKey === library.key);
        const folders = foldersOf(state, library);
        return (
          <Section key={library.key} title={library.libraryId !== undefined ? t("setup.review.useLibrary", { name: library.name }) : t("setup.review.makeLibrary", { name: library.name })}>
            <p className="font-mono text-xs text-ink-muted">{folders.join("  ") || t("setup.review.noFolders")}</p>
            <p>{t("setup.review.quality", { quality: library.libraryId !== undefined ? starterLabel("keep") : starterLabel(library.starter, connection?.app) })}</p>
            {connection && (
              <ImportFigures
                connection={connection}
                libraryId={libraryIdOf(library)}
                takeNaming={library.libraryId === undefined ? library.takeNaming !== false : undefined}
                onTakeNaming={(takeNaming) => update(replaceLibrary(state, library.key, { takeNaming }))}
              />
            )}
          </Section>
        );
      })}
      {prowlarr.map((connection) => (
        <Section key={connection.instanceId} title={t("setup.review.prowlarr", { name: connection.name })}>
          <p>{t("setup.review.prowlarrDoes")}</p>
        </Section>
      ))}
      {content.length > 0 && <p className="text-xs text-ink-muted">{t("setup.review.later")}</p>}
    </StepFrame>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-1 rounded-md border border-border-subtle p-3 text-sm">
      <h3 className="font-medium">{title}</h3>
      {children}
    </section>
  );
}

function ImportFigures({
  connection, libraryId, takeNaming, onTakeNaming,
}: { connection: WizardConnection; libraryId: number | undefined; takeNaming: boolean | undefined; onTakeNaming: (take: boolean) => void }) {
  const options = syncOptions(connection, libraryId);
  const preview = useQuery({
    queryKey: ["setup-preview", connection.instanceId, options],
    retry: false,
    queryFn: () => ok(api.POST("/arr/instances/{id}/preview", { params: { path: { id: connection.instanceId } }, body: options })),
  });
  if (preview.isPending) return <p className="text-xs text-ink-muted">{t("setup.review.reading", { app: appTitle(connection.app) })}</p>;
  if (preview.isError) return <Verdict ok={false}>{t("setup.review.unreadable", { app: appTitle(connection.app) })}</Verdict>;
  const roots = preview.data.roots;
  const chosen = roots.reduce((sum, root) => sum + root.chosen, 0);
  const added = roots.reduce((sum, root) => sum + root.new, 0);
  const setup = preview.data.setup;
  const naming = (preview.data.settings.find((one) => one.library_id === libraryId)?.naming_changes ?? preview.data.naming_changes ?? []);
  return (
    <>
    {naming.length > 0 && takeNaming !== undefined && (
      <div className="space-y-1 text-xs">
        <label className="flex items-center gap-2 font-medium text-ink">
          <input type="checkbox" checked={takeNaming} onChange={(event) => onTakeNaming(event.target.checked)} />
          {t("setup.review.takeNaming", { app: appTitle(connection.app) })}
        </label>
        <NamingChanges changes={naming} />
      </div>
    )}
    <ul className="list-disc pl-5 text-xs text-ink-muted">
      {(connection.choices.monitored || connection.choices.unmonitored) && (
        <li>{t("setup.review.items", { chosen, added, folders: roots.length })}</li>
      )}
      {setup && connection.choices.indexers && <li>{t("setup.review.indexers", { count: setup.indexers, keyless: setup.indexers_without_key })}</li>}
      {setup && connection.choices.downloadClients && <li>{t("setup.review.clients", { count: setup.download_clients })}</li>}
      {setup && connection.choices.importLists && <li>{t("setup.review.lists", { count: setup.import_lists })}</li>}
      {setup?.problem && <li className="text-warn">{setup.problem}</li>}
      {connection.push && <li>{t("setup.review.push", { app: appTitle(connection.app) })}</li>}
    </ul>
    </>
  );
}
