import type { components } from "@/api/schema";
import { useLibraries, useQualityProfiles } from "@/lib/resources";
import { useLibraryLabel } from "@/lib/libraryLabel";

type Preview = components["schemas"]["ArrImportPreview"];

const input = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs";
const cell = "border-b border-border-subtle px-2 py-1";

/** The chosen Solarr profile per *Arr profile; null is the library's own. */
export type ProfileChoice = Record<number, number | null>;

/** ADR-52's preview and mapping step: what the import would do per root folder, which Solarr profile each *Arr
 *  profile's items take, and the libraries that take the *Arr's Media Management. Nothing is changed until Import. */
export function ArrImportPreviewPanel({
  preview,
  appName,
  mediaType,
  profiles,
  onProfiles,
  settingsInto,
  onSettingsInto,
}: {
  preview: Preview;
  appName: string;
  mediaType: string;
  profiles: ProfileChoice;
  onProfiles: (choice: ProfileChoice) => void;
  settingsInto: number[];
  onSettingsInto: (libraries: number[]) => void;
}) {
  const libraries = useLibraries();
  const qualityProfiles = useQualityProfiles();
  const libraryLabel = useLibraryLabel();
  const libraryName = (id: number | null | undefined) => {
    const library = (libraries.data ?? []).find((entry) => entry.id === id);
    return library ? libraryLabel(library) : undefined;
  };
  const sum = (key: "chosen" | "new" | "in_library" | "without_id", placed: boolean) =>
    preview.roots.filter((root) => (root.library_id != null) === placed).reduce((total, root) => total + root[key], 0);
  const unplaced = sum("chosen", false);
  const ownProfiles = (qualityProfiles.data ?? []).filter((profile) => profile.media_type === mediaType);

  return (
    <div className="flex flex-col gap-3 rounded-md border border-border-subtle bg-surface p-3">
      <p className="text-ink">
        {sum("new", true)} {preview.item_kind} would be added, {sum("in_library", true)} already in Solarr are matched and
        left as they are{sum("without_id", true) > 0 && `, ${sum("without_id", true)} without an id are skipped`}
        {unplaced > 0 && `; ${unplaced} under a folder no library uses are skipped`}.
      </p>

      <table className="w-full text-left">
        <thead className="text-ink-muted">
          <tr>
            <th className={cell}>{appName}'s folder</th>
            <th className={cell}>Here</th>
            <th className={cell}>Library</th>
            <th className={`${cell} text-right`}>New</th>
            <th className={`${cell} text-right`}>In Solarr</th>
            <th className={`${cell} text-right`}>No id</th>
          </tr>
        </thead>
        <tbody>
          {preview.roots.map((root) => (
            <tr key={root.path}>
              <td className={`${cell} font-mono`}>{root.path}</td>
              <td className={`${cell} font-mono`}>{root.local_path}</td>
              <td className={cell}>
                {libraryName(root.library_id) ?? (
                  <span className="text-warn">{root.chosen > 0 ? "No library uses this folder: add it above" : "None"}</span>
                )}
                {root.library_id != null && root.root_folder_id == null && <span className="text-ink-muted"> (not a root folder here yet)</span>}
              </td>
              <td className={`${cell} text-right tabular-nums`}>{root.new}</td>
              <td className={`${cell} text-right tabular-nums`}>{root.in_library}</td>
              <td className={`${cell} text-right tabular-nums`}>{root.without_id}</td>
            </tr>
          ))}
        </tbody>
      </table>

      {preview.profiles.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="font-medium text-ink">Quality profiles</span>
          <span className="text-ink-muted">
            {appName}'s profiles do not translate one to one: choose the Solarr profile each one's {preview.item_kind} take.
          </span>
          {preview.profiles.map((profile) => (
            <label key={profile.arr_profile_id} className="flex items-center gap-2">
              <span className="w-48 truncate">
                {profile.name} <span className="text-ink-muted">({profile.items})</span>
              </span>
              <span className="text-ink-muted">→</span>
              <select
                aria-label={`Solarr profile for ${profile.name}`}
                value={profiles[profile.arr_profile_id] ?? ""}
                onChange={(event) =>
                  onProfiles({ ...profiles, [profile.arr_profile_id]: event.target.value ? Number(event.target.value) : null })
                }
                className={input}
              >
                <option value="">The library's own</option>
                {ownProfiles.map((own) => (
                  <option key={own.id} value={own.id}>
                    {own.name}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
      )}

      {preview.settings.map((library) => (
        <div key={library.library_id} className="flex flex-col gap-1">
          <label className="flex items-center gap-2 font-medium text-ink">
            <input
              type="checkbox"
              disabled={library.changes.length === 0}
              checked={settingsInto.includes(library.library_id)}
              onChange={(event) =>
                onSettingsInto(
                  event.target.checked
                    ? [...settingsInto, library.library_id]
                    : settingsInto.filter((id) => id !== library.library_id),
                )
              }
            />
            Take {appName}'s Media Management into {libraryName(library.library_id) ?? "the library"}
            {library.changes.length === 0 && <span className="font-normal text-ink-muted"> (already the same)</span>}
          </label>
          {library.changes.length > 0 && (
            <ul className="pl-6 text-ink-muted">
              {library.changes.map((change) => (
                <li key={change.key}>
                  {change.label}: {change.current} → <span className="text-ink">{change.proposed}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      ))}
    </div>
  );
}
