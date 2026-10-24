import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { useCategories, useLanguageProfiles, useQualityProfiles } from "@/lib/resources";
import { useMediaType, useMediaTypes } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";

import { Card } from "./SettingsCard";
import { AddLibraryForm } from "./NewLibrary";
import { KindSelect } from "./LibraryKindSelect";

type Category = components["schemas"]["LibraryCategory"];
type CategorySettings = components["schemas"]["CategorySettings"];
type MonitorDiscovered = components["schemas"]["CategorySettings"]["monitor_discovered"];
type TVMonitor = NonNullable<components["schemas"]["CategorySettings"]["tv_monitor"]>;

// The limits a library may set, as the youngest viewer age a work admits and the ratings that are that age.
const RATING_CHOICES: readonly [number | null, string][] = [
  [null, "No limit"],
  [0, "All ages (G, TV-Y, TV-G, FSK 0)"],
  [7, "7 (PG, TV-Y7)"],
  [10, "10 (TV-PG)"],
  [12, "12 (12A, FSK 12)"],
  [13, "13 (PG-13)"],
  [14, "14 (TV-14)"],
  [16, "16 (FSK 16)"],
  [17, "17 (R, TV-MA)"],
  [18, "18 (NC-17, FSK 18)"],
];

/** The policy boundary for naming, profiles and access (ADR-27). Section 21.14's
 *  "no POST /categories at all" gap: standing one up used to need a hand-written
 *  settings/import YAML document. Editing (todo 4.6's own remaining gap, landed
 *  2026-09-12) reads/writes CategorySettings, a richer administrator-only shape
 *  than the LibraryCategory this list itself renders from -- see GetCategory's
 *  own doc comment for why the two stay separate. */
export function Categories() {
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<number | null>(null);
  const categories = useCategories();

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["categories"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/categories/{id}", { params: { path: { id } } }));
    },
    onSuccess: invalidate,
  });

  const [searchMessages, setSearchMessages] = useState<Record<number, string>>({});
  const search = useMutation({
    mutationFn: async (id: number) => {
      const { data, error } = await api.POST("/categories/{id}/search", { params: { path: { id } } });
      if (error) throw error;
      return { id, duplicate: data!.duplicate };
    },
    onSuccess: ({ id, duplicate }) =>
      setSearchMessages((prev) => ({
        ...prev,
        [id]: duplicate ? "Already running" : "Search started",
      })),
  });

  // A non-administrator cannot read this list at all (403); render nothing
  // rather than an error, the same convention Users.tsx uses.
  if (categories.error !== null) return null;

  return (
    <Card title="Every library" help="A library holds one or more kinds of media (Anime: series and movies), each kind in its own root folders with its own quality, naming and searching; name, priority, maximum rating and access are the library's.">
      {categories.data && categories.data.length > 0 ? (
        <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {libraryOrder(categories.data).map((category) =>
            editingId === category.id ? (
              <li key={category.id} className="p-3">
                <EditCategoryForm
                  categoryId={category.id}
                  isFurtherType={category.library_id != null}
                  onDone={() => {
                    setEditingId(null);
                    invalidate();
                  }}
                  onCancel={() => setEditingId(null)}
                />
              </li>
            ) : (
              <Row
                key={category.id}
                category={category}
                heldTypes={categories.data.filter((other) => other.id === category.id || other.library_id === category.id).map((other) => other.media_type)}
                onEdit={() => setEditingId(category.id)}
                onRemove={() => remove.mutate(category.id)}
                busy={remove.isPending}
                onSearch={() => search.mutate(category.id)}
                searchPending={search.isPending && search.variables === category.id}
                searchMessage={searchMessages[category.id]}
              />
            ),
          )}
        </ul>
      ) : (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          No category is configured yet.
        </p>
      )}

      {remove.isError && (
        <p className="text-xs text-danger">
          That category could not be removed -- it is still referenced by a root folder, a
          user's access grant, or a discovery job.
        </p>
      )}

      <AddLibraryForm />
    </Card>
  );
}

/** Each library followed by the further media types it holds. */
function libraryOrder(categories: readonly Category[]): Category[] {
  return categories
    .filter((category) => category.library_id == null)
    .flatMap((library) => [library, ...categories.filter((category) => category.library_id === library.id)]);
}

/** Let a library hold a further media type: a select of the types it does not hold yet. */
function AddLibraryType({ libraryId, held }: { libraryId: number; held: readonly string[] }) {
  const queryClient = useQueryClient();
  const missing = useMediaTypes().filter((info) => !held.includes(info.key));
  const add = useMutation({
    mutationFn: async (mediaType: string) => {
      await ok(api.POST("/categories/{id}/types", { params: { path: { id: libraryId } }, body: { media_type: mediaType } }));
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["categories"] });
      void queryClient.invalidateQueries({ queryKey: ["libraries"] });
    },
  });
  if (missing.length === 0) return null;
  return (
    <select
      aria-label="Also holds"
      value=""
      disabled={add.isPending}
      onChange={(event) => event.target.value && add.mutate(event.target.value)}
      className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink-muted"
    >
      <option value="">Also holds…</option>
      {missing.map((info) => (
        <option key={info.key} value={info.key}>
          {info.work.many}
        </option>
      ))}
    </select>
  );
}

function Row({
  category,
  heldTypes,
  onEdit,
  onRemove,
  busy,
  onSearch,
  searchPending,
  searchMessage,
}: {
  category: Category;
  heldTypes: readonly string[];
  onEdit: () => void;
  onRemove: () => void;
  busy: boolean;
  onSearch: () => void;
  searchPending: boolean;
  searchMessage: string | undefined;
}) {
  const mediaType = useMediaType(category.media_type);
  const further = category.library_id != null;
  return (
    <li className={`flex items-center gap-3 py-2.5 pr-3 ${further ? "pl-8" : "pl-3"}`}>
      {further ? <span className="text-sm text-ink-muted">also holds</span> : <span className="text-sm font-medium">{category.name}</span>}
      <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem]
                       text-ink-muted">
        {mediaTypeName(category.media_type)}
      </span>
      {category.works != null && (
        <span className="ml-auto shrink-0 text-xs text-ink-muted">
          {category.works} {mediaType?.work.many.toLowerCase() ?? "works"}
        </span>
      )}
      {mediaType?.searches_library && (
        <>
          {searchMessage && <span className="text-xs text-ink-muted">{searchMessage}</span>}
          <button
            type="button"
            onClick={onSearch}
            disabled={searchPending}
            title="Search every monitored, incomplete album in this library"
            className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                       hover:text-ink disabled:opacity-50 focus-visible:outline-2
                       focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {searchPending ? "…" : "Search"}
          </button>
        </>
      )}
      {!further && <AddLibraryType libraryId={category.id} held={heldTypes} />}
      <button
        type="button"
        onClick={onEdit}
        className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                   hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        Edit
      </button>
      <button
        type="button"
        aria-label={`Remove ${category.name}`}
        onClick={onRemove}
        disabled={busy}
        className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger
                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        <Trash2 aria-hidden className="size-3.5" />
      </button>
    </li>
  );
}

// Lazy-loaded on "Edit": the list itself renders from the lean LibraryCategory,
// which carries none of priority_rank/the two profile ids/monitor_discovered.
export function EditCategoryForm({
  categoryId,
  isFurtherType = false,
  onDone,
  onCancel,
}: {
  categoryId: number;
  isFurtherType?: boolean;
  onDone: () => void;
  onCancel: () => void;
}) {
  const detail = useQuery({
    queryKey: ["category", categoryId],
    queryFn: () => ok(api.GET("/categories/{id}", { params: { path: { id: categoryId } } })),
  });
  const qualityProfiles = useQualityProfiles();
  const languageProfiles = useLanguageProfiles();

  if (detail.isLoading) return <p className="text-xs text-ink-muted">Loading…</p>;
  if (detail.error !== null || !detail.data) {
    return <p className="text-xs text-danger">This library could not be read.</p>;
  }

  return (
    <EditCategoryFields
      categoryId={categoryId}
      isFurtherType={isFurtherType}
      initial={detail.data}
      qualityProfiles={qualityProfiles.data ?? []}
      languageProfiles={languageProfiles.data ?? []}
      onDone={onDone}
      onCancel={onCancel}
    />
  );
}

function EditCategoryFields({
  categoryId,
  isFurtherType,
  initial,
  qualityProfiles,
  languageProfiles,
  onDone,
  onCancel,
}: {
  categoryId: number;
  isFurtherType: boolean;
  initial: CategorySettings;
  qualityProfiles: components["schemas"]["QualityProfile"][];
  languageProfiles: components["schemas"]["LanguageProfile"][];
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial.name);
  const [priorityRank, setPriorityRank] = useState(initial.priority_rank);
  const [qualityProfileId, setQualityProfileId] = useState(initial.quality_profile_id ?? 0);
  const [languageProfileId, setLanguageProfileId] = useState(initial.language_profile_id ?? 0);
  const [monitorDiscovered, setMonitorDiscovered] = useState<MonitorDiscovered>(initial.monitor_discovered);
  const [tvMonitor, setTvMonitor] = useState<TVMonitor>(initial.tv_monitor ?? "existing");
  const [kind, setKind] = useState(initial.kind ?? "");
  const [maxAge, setMaxAge] = useState<number | null>(initial.rating_limit.max_age ?? null);
  const [allowUnrated, setAllowUnrated] = useState(initial.rating_limit.allow_unrated);
  const info = useMediaType(initial.media_type);
  const scanMonitor = info?.scan_monitor;
  const monitorsParts = scanMonitor?.setting === "tv_monitor";
  const queryClient = useQueryClient();

  const save = useMutation({
    mutationFn: async () => {
      await ok(api.PATCH("/categories/{id}", {
        params: { path: { id: categoryId } },
        body: {
          name,
          priority_rank: priorityRank,
          quality_profile_id: qualityProfileId || undefined,
          language_profile_id: languageProfileId || undefined,
          monitor_discovered: monitorDiscovered,
          tv_monitor: monitorsParts ? tvMonitor : undefined,
          kind: kind || undefined,
          rating_limit: info?.ratings && !isFurtherType ? { max_age: maxAge, allow_unrated: allowUnrated } : undefined,
        },
      }));
    },
    onSuccess: () => {
      // The sidebar tree reads "libraries", not "categories"; a rename must reach both.
      void queryClient.invalidateQueries({ queryKey: ["libraries"] });
      void queryClient.invalidateQueries({ queryKey: ["categories"] });
      void queryClient.invalidateQueries({ queryKey: ["category", categoryId] });
      onDone();
    },
  });

  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <div className="grid grid-cols-2 gap-2">
        {!isFurtherType && (
        <label className={labelStyle}>
          Name
          <input
            className={inputStyle}
            value={name}
            autoComplete="off"
            autoFocus
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        )}
        {!isFurtherType && (
        <label className={labelStyle}>
          Priority rank
          <input
            className={inputStyle}
            type="number"
            min={0}
            value={priorityRank}
            onChange={(event) => setPriorityRank(Number(event.target.value))}
          />
        </label>
        )}
        <label className={labelStyle}>
          Quality profile
          <select
            className={inputStyle}
            value={qualityProfileId}
            onChange={(event) => setQualityProfileId(Number(event.target.value))}
          >
            <option value={0}>None</option>
            {qualityProfiles.map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name}
              </option>
            ))}
          </select>
        </label>
        <label className={labelStyle}>
          Language profile
          <select
            className={inputStyle}
            value={languageProfileId}
            onChange={(event) => setLanguageProfileId(Number(event.target.value))}
          >
            <option value={0}>None</option>
            {languageProfiles.map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name}
              </option>
            ))}
          </select>
        </label>
        <KindSelect mediaType={initial.media_type} value={kind} onChange={setKind} />
        {scanMonitor && (
          <label className={labelStyle}>
            {scanMonitor.label}
            <select
              className={inputStyle}
              value={monitorsParts ? tvMonitor : monitorDiscovered}
              onChange={(event) =>
                monitorsParts
                  ? setTvMonitor(event.target.value as TVMonitor)
                  : setMonitorDiscovered(event.target.value as MonitorDiscovered)
              }
            >
              {scanMonitor.options.map((option) => (
                <option key={option.key} value={option.key}>
                  {option.label}
                </option>
              ))}
            </select>
          </label>
        )}
        {info?.ratings && !isFurtherType && (
          <label className={labelStyle}>
            Maximum rating
            <select
              className={inputStyle}
              value={maxAge ?? ""}
              onChange={(event) => setMaxAge(event.target.value === "" ? null : Number(event.target.value))}
            >
              {RATING_CHOICES.map(([age, label]) => (
                <option key={label} value={age ?? ""}>
                  {label}
                </option>
              ))}
            </select>
          </label>
        )}
        {info?.ratings && !isFurtherType && maxAge !== null && (
          <label className="flex items-center gap-2 self-end pb-2 text-sm">
            <input type="checkbox" checked={allowUnrated} onChange={(event) => setAllowUnrated(event.target.checked)} />
            Also allow unrated
          </label>
        )}
      </div>
      {info?.ratings && !isFurtherType && maxAge !== null && (
        <p className="text-xs text-ink-muted">
          Anything rated higher{allowUnrated ? "" : ", or not rated at all,"} stays where it is but is never searched in this library; a
          notice says how many, and Above rating in the library lists them.
        </p>
      )}
      {save.isError && (
        <p className="text-xs text-danger">
          {(save.error as { detail?: string }).detail ?? "That library could not be saved."}
        </p>
      )}
      <div className="flex gap-2">
        <Button
          variant="primary"
          type="submit"
          disabled={!name.trim() || save.isPending}
        >
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                     hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}
