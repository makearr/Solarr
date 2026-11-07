import { useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { t, vocabularyOf } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { invalidatePeopleLists, invalidateWorkLists, useLibraries, useQualityProfiles, useRootFolders } from "@/lib/resources";
import { useLibraryLabel } from "@/lib/libraryLabel";
import { targetTitle } from "@/lib/targetDisplay";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";
import { Image } from "@/ui/Image";

import { InteractiveSearchResults } from "../InteractiveSearch";
import { useLibraryQuality } from "../LibraryTargetSelect";
import { TagPicker } from "../TagChips";
import { AddPriority } from "./AddPriority";

type Candidate = components["schemas"]["AddCandidate"];
type LibraryEntry = components["schemas"]["Library"];
type AddResult = components["schemas"]["AddWorkResult"];

/** What the dialog needs of a thing to add; the lookup's candidate and a person's known-for title both fit. */
export type AddSubject = Pick<Candidate, "provider" | "external_id" | "title"> & Partial<Pick<Candidate, "poster_url" | "subtitle" | "year" | "suggestion">> & { kind?: string };

const FIELD = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm";
const LABEL = "w-28 shrink-0 text-xs text-ink-muted";

/** The one way to add something new: where it goes, at what quality, monitored or not, then search now or by hand.
 *  Used from search, discover, collections and person pages. */
export function AddToSolarr({
  candidate,
  categoryId: firstCategoryId,
  libraries,
  info,
  person = false,
  onAdded,
  onClose,
}: {
  candidate: AddSubject;
  categoryId: string;
  libraries?: readonly LibraryEntry[];
  info: MediaTypeInfo;
  person?: boolean;
  onAdded: (result: AddResult) => void;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const libraryLabel = useLibraryLabel();
  const ownLibrary = useLibrary(firstCategoryId);
  // The libraries' rules suggest where it goes (ADR-56); always shown with its reason, always changeable.
  const suggested = candidate.suggestion && libraries?.some((library) => library.id === candidate.suggestion!.category_id) ? candidate.suggestion : undefined;
  const [categoryId, setCategoryId] = useState(suggested ? String(suggested.category_id) : firstCategoryId);
  const words = vocabularyOf(info);
  const options = info.monitor_options;
  const [monitored, setMonitored] = useState(true);
  const [option, setOption] = useState(options[0]?.key ?? "");
  const [specials, setSpecials] = useState(false);
  const [profileId, setProfileId] = useState(0);
  const [rootFolderId, setRootFolderId] = useState(0);
  const [tags, setTags] = useState<string[]>([]);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [chosenTarget, setChosenTarget] = useState<string>();
  const [manual, setManual] = useState<AddResult>();
  const finished = useRef(false);
  const profiles = useQualityProfiles();
  const { quality } = useLibraryQuality(Number(categoryId));
  const editable = info.facets.filter((facet) => facet.editable && facet.kind === "enum" && facet.key !== "air_time_zone");
  const libraryRoots = (useRootFolders().data ?? []).filter((root) => (root.categories ?? []).some((category) => category.id === Number(categoryId)));
  // Request-only access files a request an administrator approves instead of adding (Seerr's model).
  const requestOnly = (libraries?.find((library) => String(library.id) === categoryId) ?? ownLibrary)?.permission === "request";
  const targets = quality?.available_targets ?? [];
  const libraryTarget = quality ? (quality.target_auto ? "auto" : String(quality.target?.id ?? "")) : "";
  const target = chosenTarget ?? libraryTarget;
  // The provider's word for the kind ("tv") is not always the type's add kind ("series").
  const kind = info.add_kinds.some((entry) => entry.key === candidate.kind) ? candidate.kind : (info.add_kinds[0]?.key ?? "");

  const add = useMutation({
    mutationFn: async (mode: "add" | "now" | "manual") => {
      // Another target than the library's is this work's own.
      const ownTarget = !person && target !== libraryTarget && target !== "" && target !== "auto" ? Number(target) : undefined;
      const watch = mode === "manual" ? false : monitored;
      const { data, error } = await api.POST("/works", {
        body: {
          category_id: Number(categoryId), kind, provider: candidate.provider, external_id: candidate.external_id,
          title: candidate.title,
          monitored: options.length > 0 ? watch && option !== "none" : watch,
          monitor_option: options.length > 0 ? (watch ? option : "none") : undefined,
          specials, quality_profile_id: profileId || undefined, target_id: ownTarget, search: mode === "now",
          root_folder_id: rootFolderId || undefined,
          tags: tags.length > 0 ? tags : undefined,
          fields: Object.fromEntries(Object.entries(fields).filter(([, value]) => value !== "")),
        },
      });
      if (error) throw error;
      // A type that does not search on its own is searched as a whole work, season by season for a series.
      if (mode === "now" && !data.searching && data.work_id && !requestOnly) {
        void api.POST("/works/{id}/grab", { params: { path: { id: data.work_id } } });
      }
      return { data, mode };
    },
    onSuccess: ({ data, mode }) => {
      invalidateWorkLists(queryClient);
      invalidatePeopleLists(queryClient);
      if (mode === "manual" && data.work_id) {
        setManual(data);
        return;
      }
      onAdded(data);
      if (!data.request_id) onClose();
    },
  });

  // A manual add was unmonitored so nothing searched on its own; the box is honoured once the person is done.
  const finishManual = async () => {
    if (!manual || finished.current) return;
    finished.current = true;
    if (monitored && manual.work_id) {
      // "Monitor which" (the first season, the future ones) is applied as the Edit dialog applies it; the checkbox alone otherwise.
      const withOption = options.length > 0 && option !== "none";
      await api.POST("/works/{id}/monitor", {
        params: { path: { id: manual.work_id } },
        body: withOption ? { option, specials } : { monitored: true, search: false },
      });
      invalidateWorkLists(queryClient);
    }
    onAdded(manual);
  };
  const close = () => {
    void finishManual();
    onClose();
  };

  const head = (
    <div className="flex gap-3">
      {candidate.poster_url && (
        <div className="w-16 shrink-0">
          <Image shape="poster" title={candidate.title} src={remoteImage(candidate.poster_url, "thumb")} />
        </div>
      )}
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm font-semibold">{candidate.title}</span>
        <span className="text-xs text-ink-muted">{[candidate.subtitle, candidate.year].filter(Boolean).join(" · ")}</span>
      </div>
    </div>
  );

  if (manual?.work_id) {
    return (
      <Dialog width="wide" title={t("addDialog.manualTitle", { title: candidate.title })} description={t("addDialog.manualHelp")} onClose={close}
        actions={<Button variant="primary" onClick={close}>{t("addDialog.done")}</Button>}>
        <InteractiveSearchResults workId={manual.work_id} />
      </Dialog>
    );
  }

  const busy = add.isPending;
  return (
    <Dialog
      title={requestOnly ? t("addDialog.requestTitle") : t("collection.request")}
      onClose={onClose}
      actions={
        add.data?.data.request_id ? (
          <span className="text-xs text-ok">Requested: an administrator will look at it. See Activity, Requests.</span>
        ) : (
          <>
            <Button onClick={onClose}>{t("action.cancel")}</Button>
            {!requestOnly && !person && (
              <>
                <Button
                  variant="primary"
                  title={t("addDialog.addHelp")}
                  busy={busy && add.variables === "add"}
                  busyLabel={t("addDialog.adding")}
                  disabled={busy}
                  onClick={() => add.mutate("add")}
                >
                  {t("addDialog.add")}
                </Button>
                <Button title={t("addDialog.searchManuallyHelp")} busy={busy && add.variables === "manual"} busyLabel={t("addDialog.adding")} disabled={busy} onClick={() => add.mutate("manual")}>
                  {t("addDialog.searchManually")}
                </Button>
                <Button title={t("addDialog.searchNowHelp")} busy={busy && add.variables === "now"} busyLabel={t("addDialog.adding")} disabled={busy} onClick={() => add.mutate("now")}>
                  {t("addDialog.searchNow")}
                </Button>
              </>
            )}
            {(requestOnly || person) && (
              <Button variant="primary" busy={busy && add.variables === "now"} busyLabel={t("addDialog.adding")} disabled={busy} onClick={() => add.mutate("now")}>
                {requestOnly ? t("addDialog.request") : t("addDialog.follow")}
              </Button>
            )}
          </>
        )
      }
    >
      <div className="flex flex-col gap-3 text-sm">
        {head}
        {libraries && libraries.length > 1 && (
          <label className="flex flex-wrap items-center gap-2">
            <span className={LABEL}>{t("addDialog.library")}</span>
            <select value={categoryId} onChange={(event) => { setCategoryId(event.target.value); setChosenTarget(undefined); setRootFolderId(0); }} className={FIELD}>
              {libraries.map((library) => (
                <option key={library.id} value={String(library.id)}>
                  {libraryLabel(library)}
                </option>
              ))}
            </select>
            {suggested && (
              <span className="text-xs text-ink-muted">
                {String(suggested.category_id) === categoryId ? "Suggested" : `${suggested.name} suggested`}: {suggested.reason}
              </span>
            )}
          </label>
        )}
        {!person && quality && (targets.length > 0 || quality.target) && (
          <label className="flex flex-wrap items-center gap-2">
            <span className={LABEL}>{t("addDialog.target")}</span>
            <select value={target} onChange={(event) => setChosenTarget(event.target.value)} className={FIELD}>
              {libraryTarget === "" && <option value="">{t("addDialog.noTarget")}</option>}
              {libraryTarget === "auto" && <option value="auto">{t("addDialog.autoTarget")}</option>}
              {targets.map((entry) => (
                <option key={entry.id} value={String(entry.id)}>
                  {targetTitle(entry)}
                </option>
              ))}
            </select>
            {target !== libraryTarget && <span className="text-xs text-ink-muted">{t("addDialog.targetOwn")}</span>}
          </label>
        )}
        {!person && info.key !== "music" && libraryRoots.length > 1 && (
          <label className="flex flex-wrap items-center gap-2">
            <span className={LABEL}>{t("addDialog.rootFolder")}</span>
            <select value={rootFolderId} onChange={(event) => setRootFolderId(Number(event.target.value))} className={FIELD}>
              <option value={0}>{t("addDialog.rootAutomatic")}</option>
              {libraryRoots.map((root) => (
                <option key={root.id} value={root.id}>
                  {root.path}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="flex items-center gap-2 text-xs text-ink-muted">
          <input type="checkbox" checked={monitored} onChange={(event) => setMonitored(event.target.checked)} />
          {person ? `Monitor: add their ${words.works} and follow new ones` : t("action.monitor")}
        </label>
        {!person && (
          <details className="text-xs">
            <summary className="cursor-pointer text-ink-muted">{t("addDialog.more")}</summary>
            <div className="mt-2 flex flex-col gap-2">
              {options.length > 0 && (
                <label className="flex flex-wrap items-center gap-2">
                  <span className={LABEL}>{t("addDialog.monitorWhich")}</span>
                  <select value={option} disabled={!monitored} onChange={(event) => setOption(event.target.value)} className={FIELD}>
                    {options.map((entry) => (
                      <option key={entry.key} value={entry.key}>
                        {entry.label}
                      </option>
                    ))}
                  </select>
                  <label className="flex items-center gap-1 text-ink-muted">
                    <input type="checkbox" checked={specials} onChange={(event) => setSpecials(event.target.checked)} /> also the specials
                  </label>
                </label>
              )}
              <label className="flex flex-wrap items-center gap-2">
                <span className={LABEL}>{t("addDialog.profile")}</span>
                <select value={profileId} onChange={(event) => setProfileId(Number(event.target.value))} className={FIELD}>
                  <option value={0}>The library&apos;s default</option>
                  {(profiles.data ?? [])
                    .filter((profile) => profile.media_type === info.key)
                    .map((profile) => (
                      <option key={profile.id} value={profile.id}>
                        {profile.name}
                      </option>
                    ))}
                </select>
              </label>
              {editable.map((facet) => (
                <label key={facet.key} className="flex flex-wrap items-center gap-2">
                  <span className={LABEL}>{facet.label}</span>
                  <select value={fields[facet.key] ?? ""} onChange={(event) => setFields({ ...fields, [facet.key]: event.target.value })} className={FIELD}>
                    <option value="">Automatic</option>
                    {(facet.choices ?? []).map((choice) => (
                      <option key={choice.key} value={choice.key}>
                        {choice.label}
                      </option>
                    ))}
                  </select>
                </label>
              ))}
              <TagPicker label="Tags" genreTree={info.genre_tree.key} names={tags} onChange={setTags} />
              <AddPriority categoryId={Number(categoryId)} />
            </div>
          </details>
        )}
        {add.isError && <span className="text-xs text-danger">{(add.error as { detail?: string }).detail ?? "It could not be added. Try again."}</span>}
        {add.data?.data.work_id && add.data.data.request_id === undefined && (
          <Link to="/library/$categoryId/works/$workId" params={{ categoryId, workId: String(add.data.data.work_id) }} className="text-xs text-ok">
            Added: open it
          </Link>
        )}
      </div>
    </Dialog>
  );
}

/** The dialog for a thing found elsewhere (a person's known-for titles, a collection's missing items): the libraries
 *  holding its type are offered, the first one given preselected. */
export function AddToLibrary({
  candidate,
  categoryId,
  onDone,
}: {
  candidate: AddSubject;
  categoryId: string;
  onDone: () => void;
}) {
  const library = useLibrary(categoryId);
  const mediaTypes = useMediaTypes();
  const all = useLibraries().data ?? [];
  const info = mediaTypes.find((entry) => entry.key === library?.media_type);
  if (!info || !library) return null;
  const sameType = all.filter((entry) => entry.media_type === library.media_type);
  return <AddToSolarr candidate={candidate} categoryId={categoryId} libraries={sameType} info={info} onAdded={onDone} onClose={onDone} />;
}
