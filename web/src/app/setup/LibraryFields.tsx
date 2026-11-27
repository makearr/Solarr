import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { useCategories } from "@/lib/resources";
import { useMediaTypes } from "@/lib/useMediaTypes";

import { KindSelect } from "../LibraryKindSelect";
import { PathPicker } from "../PathPicker";
import { useDebouncedValue } from "../useDebouncedValue";
import { Field, Verdict, inputStyle, quietButtonStyle } from "./ui";
import type { WizardLibrary } from "./wizardState";

/** Name, type and kind of a library, or an existing library of that type to use instead (never changing it). */
export function LibraryFields({ library, onChange }: { library: WizardLibrary; onChange: (change: Partial<WizardLibrary>) => void }) {
  const mediaTypes = useMediaTypes();
  const categories = useCategories();
  const existing = (categories.data ?? []).filter((category) => category.media_type === library.mediaType);
  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("setup.library.type")}>
          <select
            className={inputStyle}
            value={library.mediaType}
            onChange={(event) => onChange({ mediaType: event.target.value, kind: "", libraryId: undefined })}
          >
            {mediaTypes.map((type) => (
              <option key={type.key} value={type.key}>
                {type.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("setup.library.use")}>
          <select
            className={inputStyle}
            value={library.libraryId ?? 0}
            onChange={(event) => {
              const id = Number(event.target.value);
              const picked = existing.find((category) => category.id === id);
              onChange(id === 0 ? { libraryId: undefined } : { libraryId: id, name: picked?.name ?? library.name });
            }}
          >
            <option value={0}>{t("setup.library.makeNew")}</option>
            {existing.map((category) => (
              <option key={category.id} value={category.id}>
                {category.name}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {library.libraryId === undefined && (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("setup.library.name")}>
            <input className={inputStyle} value={library.name} onChange={(event) => onChange({ name: event.target.value })} />
          </Field>
          <KindSelect mediaType={library.mediaType} value={library.kind} onChange={(kind) => onChange({ kind })} />
        </div>
      )}
    </div>
  );
}

/** Whether a folder typed here exists inside Solarr's own container, which is what matters; nothing is said while it is empty. */
export function FolderCheck({ path }: { path: string }) {
  const trimmed = useDebouncedValue(path.trim(), 400);
  const listing = useQuery({
    queryKey: ["folder-check", trimmed],
    enabled: trimmed.startsWith("/"),
    retry: false,
    queryFn: () => ok(api.GET("/storage/browse", { params: { query: { path: `${trimmed.replace(/\/+$/, "")}/`, dirs_only: true } } })),
  });
  if (!trimmed.startsWith("/")) return trimmed === "" ? null : <Verdict ok={false}>{t("setup.folder.absolute")}</Verdict>;
  if (listing.isPending) return null;
  // Browse is a typeahead: a missing folder answers with its parent's listing, so only an exact path match means found.
  const found = !listing.isError && listing.data.path === (trimmed.replace(/\/+$/, "") || "/");
  return found ? <Verdict ok>{t("setup.folder.found")}</Verdict> : <Verdict ok={false}>{t("setup.folder.missing")}</Verdict>;
}

/** One folder as the container sees it: typed or browsed, with its check. */
export function FolderField({ value, onChange, label, onRemove }: { value: string; onChange: (path: string) => void; label: string; onRemove?: () => void }) {
  return (
    <div className="space-y-1">
      <div className="flex items-start gap-2">
        <div className="flex-1">
          <PathPicker value={value} onChange={onChange} recent={[]} label={label} placeholder="/media/movies" />
        </div>
        {onRemove && (
          <button type="button" onClick={onRemove} className={quietButtonStyle}>
            {t("setup.remove")}
          </button>
        )}
      </div>
      <FolderCheck path={value} />
    </div>
  );
}
