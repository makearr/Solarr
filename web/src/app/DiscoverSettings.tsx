import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, tOr, type MessageKey } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";
import { inputStyle, labelStyle } from "@/ui/form";

import { LanguagePicker } from "./LanguagePicker";
import { Card } from "./SettingsCard";

type Library = components["schemas"]["LibraryDiscoverSettings"];

const LIST_LABELS: Record<string, MessageKey> = {
  highest_rated: "discover.list.highest_rated",
  trending: "discover.list.trending",
  upcoming: "discover.list.upcoming",
  classics: "discover.list.classics",
  collections: "discover.list.collections",
};

const toList = (text: string) => text.split(",").map((part) => part.trim()).filter((part) => part !== "");

/** Settings > Discover: the switch, what is never recommended and each library's lists and auto-add gates. */
export function DiscoverSettings() {
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ["discover-settings"], queryFn: () => ok(api.GET("/settings/discover")) });
  const [genres, setGenres] = useState("");
  const [networks, setNetworks] = useState("");
  useEffect(() => {
    if (settings.data) {
      setGenres(settings.data.global.exclude_genres.join(", "));
      setNetworks(settings.data.global.exclude_networks.join(", "));
    }
  }, [settings.data]);
  const saveGlobal = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (body: components["schemas"]["DiscoverGlobalSettings"]) => ok(api.PUT("/settings/discover", { body })),
    onSuccess: (data) => {
      queryClient.setQueryData(["discover-settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["general-settings"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-libraries"] });
    },
  });
  const global = settings.data?.global;
  return (
    <div className="flex flex-col gap-6">
      <Card title={t("discover.title")} help={t("discover.help")}>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            disabled={!global || saveGlobal.isPending}
            checked={global?.enabled ?? false}
            onChange={(event) => global && saveGlobal.mutate({ ...global, enabled: event.target.checked })}
          />
          {t("discover.switch")}
        </label>
        <label className={labelStyle}>
          <span>{t("discover.neverGenres")}</span>
          <input className={inputStyle} value={genres} onChange={(event) => setGenres(event.target.value)} />
        </label>
        <label className={labelStyle}>
          <span>{t("discover.neverNetworks")}</span>
          <input className={inputStyle} value={networks} onChange={(event) => setNetworks(event.target.value)} />
        </label>
        <div>
          <Button
            variant="primary"
            disabled={!global}
            busy={saveGlobal.isPending}
            onClick={() => global && saveGlobal.mutate({ ...global, exclude_genres: toList(genres), exclude_networks: toList(networks) })}
          >
            {t("discover.save")}
          </Button>
        </div>
      </Card>
      {settings.data?.libraries.map((library) => <LibraryCard key={library.category_id} library={library} globalOn={global?.enabled ?? false} />)}
    </div>
  );
}

function LibraryCard({ library, globalOn }: { library: Library; globalOn: boolean }) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState(library);
  const [genres, setGenres] = useState(library.exclude_genres.join(", "));
  const [networks, setNetworks] = useState(library.exclude_networks.join(", "));
  useEffect(() => {
    setDraft(library);
    setGenres(library.exclude_genres.join(", "));
    setNetworks(library.exclude_networks.join(", "));
  }, [library]);
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.PUT("/settings/discover/libraries/{category_id}", {
      params: { path: { category_id: library.category_id } },
      body: { ...draft, exclude_genres: toList(genres), exclude_networks: toList(networks) },
    })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["discover-settings"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-libraries"] });
    },
  });
  const [previewing, setPreviewing] = useState<number | null>(null);
  const number = (key: "total_amount" | "classic_age_years" | "min_votes" | "auto_add_per_day" | "min_rating", label: MessageKey) => (
    <label className={labelStyle}>
      <span>{t(label)}</span>
      <input
        type="number"
        className={inputStyle}
        value={draft[key]}
        onChange={(event) => setDraft({ ...draft, [key]: Number(event.target.value) })}
      />
    </label>
  );
  return (
    <Card
      title={library.name ?? String(library.category_id)}
      help={globalOn ? t("discover.libraryHelp") : t("discover.libraryOff")}
      toggle={
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })} />
          {t("discover.enabled")}
        </label>
      }
    >
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        {number("total_amount", "discover.totalAmount")}
        {number("classic_age_years", "discover.classicYears")}
        {number("min_rating", "discover.minRating")}
        {number("min_votes", "discover.minVotes")}
        {number("auto_add_per_day", "discover.autoAddPerDay")}
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={draft.auto_add_search} onChange={(event) => setDraft({ ...draft, auto_add_search: event.target.checked })} />
        {t("discover.autoAddSearch")}
      </label>
      <LanguagePicker
        label={t("discover.languages")}
        help={draft.languages_set ? undefined : t("discover.languagesFromProfile")}
        value={draft.original_languages}
        onChange={(next) => setDraft({ ...draft, original_languages: next, languages_set: true })}
      />
      <label className={labelStyle}>
        <span>{t("discover.libraryGenres")}</span>
        <input className={inputStyle} value={genres} onChange={(event) => setGenres(event.target.value)} />
      </label>
      <label className={labelStyle}>
        <span>{t("discover.libraryNetworks")}</span>
        <input className={inputStyle} value={networks} onChange={(event) => setNetworks(event.target.value)} />
      </label>
      <table className="text-sm">
        <thead>
          <tr className="text-left text-xs text-ink-muted">
            <th>{t("discover.listName")}</th>
            <th>{t("discover.enabled")}</th>
            <th>{t("discover.amount")}</th>
            <th>{t("discover.addAutomatically")}</th>
          </tr>
        </thead>
        <tbody>
          {draft.types.map((row, index) => {
            const change = (patch: Partial<typeof row>) => setDraft({ ...draft, types: draft.types.map((other, at) => (at === index ? { ...other, ...patch } : other)) });
            return (
              <tr key={row.type}>
                <td>{t(LIST_LABELS[row.type] ?? "discover.list.highest_rated")}</td>
                <td><input type="checkbox" aria-label={t(LIST_LABELS[row.type] ?? "discover.list.highest_rated")} checked={row.enabled} onChange={(event) => change({ enabled: event.target.checked })} /></td>
                <td><input type="number" className={inputStyle} value={row.amount} onChange={(event) => change({ amount: Number(event.target.value) })} /></td>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`${t("discover.addAutomatically")}: ${t(LIST_LABELS[row.type] ?? "discover.list.highest_rated")}`}
                    checked={row.auto_add}
                    onChange={(event) => {
                      change({ auto_add: event.target.checked });
                      if (event.target.checked) setPreviewing(index);
                    }}
                  />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <div className="flex items-center gap-2">
        <Button variant="primary" busy={save.isPending} onClick={() => save.mutate()}>{t("discover.saveRefresh")}</Button>
        {save.isError && <span className="text-xs text-danger">{(save.error as { detail?: string }).detail ?? t("discover.refused")}</span>}
      </div>
      {previewing !== null && (
        <AutoAddPreview
          library={{ ...draft, exclude_genres: toList(genres), exclude_networks: toList(networks) }}
          onConfirm={() => setPreviewing(null)}
          onCancel={() => {
            setDraft({ ...draft, types: draft.types.map((other, at) => (at === previewing ? { ...other, auto_add: false } : other)) });
            setPreviewing(null);
          }}
        />
      )}
    </Card>
  );
}

/** What auto-add would add now with the unsaved settings, shown before it is switched on (nothing is added by looking). */
function AutoAddPreview({ library, onConfirm, onCancel }: { library: Library; onConfirm: () => void; onCancel: () => void }) {
  const preview = useQuery({
    queryKey: ["discover-auto-add-preview", library],
    queryFn: () => ok(api.POST("/settings/discover/libraries/{category_id}/preview", { params: { path: { category_id: library.category_id } }, body: library })),
  });
  const data = preview.data;
  return (
    <Dialog
      title={t("discover.preview.title", { name: library.name ?? String(library.category_id) })}
      description={t("discover.preview.description")}
      width="wide"
      onClose={onCancel}
      closeLabel={t("discover.preview.cancel")}
      actions={
        <>
          <Button variant="ghost" onClick={onCancel}>{t("discover.preview.cancel")}</Button>
          <Button variant="primary" disabled={!data} onClick={onConfirm}>{t("discover.preview.confirm")}</Button>
        </>
      }
    >
      {preview.isLoading && <p className="text-sm text-ink-muted">{t("discover.preview.loading")}</p>}
      {preview.isError && <p className="text-sm text-danger">{(preview.error as { detail?: string }).detail ?? t("discover.preview.failed")}</p>}
      {data && data.items.length === 0 && <p className="text-sm text-ink-muted">{t("discover.preview.empty")}</p>}
      {data && data.items.length > 0 && (
        <div className="flex flex-col gap-2">
          <p className="text-sm font-medium">{t("discover.preview.summary", { add: data.would_add, held: data.held_back })}</p>
          <ul className="flex max-h-[50vh] flex-col gap-1 overflow-y-auto text-sm">
            {data.items.map((item, index) => (
              <li key={index} className="flex flex-col gap-0.5 border-b border-border-subtle py-1">
                <span>
                  {item.title}
                  {item.year ? ` (${item.year})` : ""}
                  <span className="ml-2 text-xs text-ink-muted">{item.rating.toFixed(1)} · {item.votes}</span>
                </span>
                {item.would_add ? (
                  <span className="text-xs text-ok">{t("discover.preview.wouldAdd")}</span>
                ) : (
                  item.rejections.map((rejection) => (
                    <span key={rejection.reason + (rejection.value ?? "")} className="text-xs text-ink-muted">
                      {tOr(`discover.reason.${rejection.reason}`, rejection.reason).replace("{value}", rejection.value ?? "")}
                    </span>
                  ))
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </Dialog>
  );
}
