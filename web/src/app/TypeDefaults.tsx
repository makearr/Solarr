import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { moveItem } from "@/lib/typeDefaults";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { inputStyle, labelStyle } from "@/ui/form";

import { Card } from "./SettingsCard";

type TypeDefaults = components["schemas"]["LibraryTypeDefaults"];
type TypeDefaultsWrite = components["schemas"]["LibraryTypeDefaultsWrite"];

const KEY = ["libraryTypeDefaults"];

/** What each media type's libraries start with until they set their own. */
export const useLibraryTypeDefaults = () =>
  useQuery({ queryKey: KEY, queryFn: async () => (await ok(api.GET("/library-type-defaults"))).types });

function useSaveTypeDefaults(mediaType: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (change: TypeDefaultsWrite) =>
      ok(api.PUT("/library-type-defaults/{mediaType}", { params: { path: { mediaType } }, body: change })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: KEY });
      // Every library of the type may follow the change: their pages read these.
      void queryClient.invalidateQueries({ queryKey: ["libraryMetadataSources"] });
      void queryClient.invalidateQueries({ queryKey: ["libraryQuality"] });
      void queryClient.invalidateQueries({ queryKey: ["category"] });
    },
  });
}

/** The providers a library of one type asks, in order: on or off and the order, which a library's own list overrides. */
export function MetadataTypeTab({ entry }: { entry: TypeDefaults }) {
  const save = useSaveTypeDefaults(entry.media_type);
  const name = mediaTypeName(entry.media_type);
  const sources = entry.metadata_sources;
  const info = (key: string) => entry.metadata_available.find((provider) => provider.key === key);
  const unused = entry.metadata_available.filter((provider) => !sources.includes(provider.key));
  const set = (next: string[]) => save.mutate({ metadata_sources: next });
  return (
    <section className="flex flex-col gap-3">
      <p className="max-w-2xl text-sm text-ink-muted">
        The providers a {name.toLowerCase()} library asks and the order it asks them in. The first that has a field supplies it; each later one only
        fills what the earlier ones left empty, and one left out is not asked. A library can keep its own list under its Settings, Metadata; until it
        does it follows this one.
      </p>
      <div className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold">Order for {name.toLowerCase()} libraries</h2>
          <Chip>{entry.metadata_custom ? "changed here" : "the shipped order"}</Chip>
          {entry.metadata_custom && (
            <Button className="ml-auto" busy={save.isPending} onClick={() => set([])}>
              Use the shipped order
            </Button>
          )}
        </div>
        <ol className="flex flex-col divide-y divide-border-subtle text-sm">
          {sources.map((key, index) => (
            <li key={key} className="flex flex-wrap items-center gap-2 py-2">
              <span className="w-5 text-right tabular-nums text-ink-muted">{index + 1}.</span>
              <span className="font-medium">{info(key)?.label ?? key}</span>
              {!info(key)?.enabled && <Chip tone="warn">switched off under Providers</Chip>}
              <span className="min-w-0 flex-1 text-xs text-ink-muted">{info(key)?.carries.join(", ")}</span>
              <Button variant="icon" aria-label={`Move ${info(key)?.label ?? key} up`} disabled={index === 0 || save.isPending} onClick={() => set(moveItem(sources, index, -1))}>
                <ArrowUp aria-hidden className="size-4" />
              </Button>
              <Button variant="icon" aria-label={`Move ${info(key)?.label ?? key} down`} disabled={index === sources.length - 1 || save.isPending} onClick={() => set(moveItem(sources, index, 1))}>
                <ArrowDown aria-hidden className="size-4" />
              </Button>
              <Button disabled={sources.length === 1 || save.isPending} onClick={() => set(sources.filter((one) => one !== key))}>
                Turn off
              </Button>
            </li>
          ))}
        </ol>
        {unused.length > 0 && (
          <div className="flex flex-col gap-1 border-t border-border-subtle pt-2 text-sm">
            <span className="text-xs text-ink-muted">Off for {name.toLowerCase()} libraries</span>
            {unused.map((provider) => (
              <div key={provider.key} className="flex flex-wrap items-center gap-2">
                <span>{provider.label}</span>
                <span className="min-w-0 flex-1 text-xs text-ink-muted">{provider.carries.join(", ")}</span>
                <Button busy={save.isPending} onClick={() => set([...sources, provider.key])}>
                  Turn on, last
                </Button>
              </div>
            ))}
          </div>
        )}
        {save.isError && <p className="text-xs text-danger">That could not be saved.</p>}
      </div>
    </section>
  );
}

/** The default target quality, language and quality profiles of every media type, which a library follows until it chooses. */
export function QualityTypeDefaults() {
  const defaults = useLibraryTypeDefaults();
  if (!defaults.data) return null;
  return (
    <div className="flex flex-col gap-4">
      <p className="max-w-2xl text-sm text-ink-muted">
        What a library of each type aims for until it chooses itself: the target quality, the language, then the quality profiles. A library&apos;s own
        choice, under Settings, Libraries or its Quality page, always wins, and can be reset to follow these.
      </p>
      {defaults.data.map((entry) => (
        <TypeQualityCard key={entry.media_type} entry={entry} />
      ))}
    </div>
  );
}

function TypeQualityCard({ entry }: { entry: TypeDefaults }) {
  const save = useSaveTypeDefaults(entry.media_type);
  const name = mediaTypeName(entry.media_type);
  const ids = entry.quality_profile_ids;
  const nameOf = (id: number) => entry.available_profiles.find((profile) => profile.id === id)?.name ?? `#${id}`;
  const addable = entry.available_profiles.filter((profile) => !ids.includes(profile.id));
  if (entry.available_targets.length === 0 && entry.available_profiles.length === 0 && entry.available_languages.length === 0) return null;
  const targetValue = entry.target_auto ? "auto" : String(entry.target_id ?? "");
  return (
    <Card title={name} help={`What a ${name.toLowerCase()} library follows until it has a choice of its own.`}>
      <div className="grid gap-3 sm:grid-cols-2">
        {entry.available_targets.length > 0 && (
          <label className={labelStyle}>
            Target quality
            <select
              className={inputStyle}
              value={targetValue}
              disabled={save.isPending}
              onChange={(event) => {
                const choice = event.target.value;
                if (choice === "auto") save.mutate({ target_auto: true });
                else save.mutate({ target_auto: false, target_id: choice === "" ? 0 : Number(choice) });
              }}
            >
              <option value="">None: libraries choose</option>
              <option value="auto" disabled={ids.length === 0}>
                Automatic (from the first profile)
              </option>
              {entry.available_targets.map((target) => (
                <option key={target.id} value={target.id}>
                  {target.name}
                </option>
              ))}
            </select>
          </label>
        )}
        {entry.available_languages.length > 0 && (
          <label className={labelStyle}>
            Language
            <select
              className={inputStyle}
              value={entry.language_profile_id ?? ""}
              disabled={save.isPending}
              onChange={(event) => save.mutate({ language_profile_id: Number(event.target.value) })}
            >
              <option value="">None: libraries choose</option>
              {entry.available_languages.map((language) => (
                <option key={language.id} value={language.id}>
                  {language.name}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      {entry.available_profiles.length > 0 && (
        <div className="flex flex-col gap-1 text-sm">
          <span className="text-xs text-ink-muted">Quality profiles, best first</span>
          {ids.length === 0 && <span className="text-xs text-ink-muted">None: libraries choose, or aim for their target alone.</span>}
          {ids.map((id, index) => (
            <div key={id} className="flex items-center gap-2">
              <span className="w-5 text-right tabular-nums text-ink-muted">{index + 1}.</span>
              <span className="flex-1">{nameOf(id)}</span>
              <Button variant="icon" aria-label={`Move ${nameOf(id)} up`} disabled={index === 0 || save.isPending} onClick={() => save.mutate({ quality_profile_ids: moveItem(ids, index, -1) })}>
                <ArrowUp aria-hidden className="size-4" />
              </Button>
              <Button variant="icon" aria-label={`Move ${nameOf(id)} down`} disabled={index === ids.length - 1 || save.isPending} onClick={() => save.mutate({ quality_profile_ids: moveItem(ids, index, 1) })}>
                <ArrowDown aria-hidden className="size-4" />
              </Button>
              <Button disabled={save.isPending} onClick={() => save.mutate({ quality_profile_ids: ids.filter((one) => one !== id) })}>
                Remove
              </Button>
            </div>
          ))}
          {addable.length > 0 && (
            <select
              aria-label={`Add a quality profile to the ${name.toLowerCase()} default`}
              className={`${inputStyle} max-w-64`}
              value=""
              disabled={save.isPending}
              onChange={(event) => event.target.value && save.mutate({ quality_profile_ids: [...ids, Number(event.target.value)] })}
            >
              <option value="">Add a profile…</option>
              {addable.map((profile) => (
                <option key={profile.id} value={profile.id}>
                  {profile.name}
                </option>
              ))}
            </select>
          )}
        </div>
      )}
      {save.isError && <p className="text-xs text-danger">That could not be saved.</p>}
    </Card>
  );
}
