import { Fragment, useState } from "react";

import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { albumStatusLabel, albumStatusText } from "@/lib/albumStatus";
import { mediaTypeIcon, mediaTypeName } from "@/lib/mediaTypeIcon";
import { remoteImage } from "@/lib/remoteImage";
import { invalidatePeopleLists, invalidateWorkLists, useLibraries } from "@/lib/resources";
import { discCountOf, formatTrackNumber } from "@/lib/trackNumber";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { workLink } from "@/lib/workLink";

import { AddToLibrary } from "./work/AddWork";
import { ChooseImage } from "./ChooseImage";
import { useAllParts } from "./work/WorkParts";
import { CoverArt } from "./CoverArt";
import { FollowPanel } from "./FollowPanel";
import { PersonPicker } from "./PersonPicker";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";
import { inputStyle, labelStyle } from "@/ui/form";

type PersonWork = components["schemas"]["PersonWork"];
type Credit = components["schemas"]["WorkCredit"];
type KnownFor = components["schemas"]["KnownFor"];
type Library = components["schemas"]["Library"];

// Real MusicBrainz release-group types, in the order Lidarr's own artist
// page groups them; anything else this library has never seen a value for
// (a future MusicBrainz type, or no primary_type at all yet) falls through
// to TYPE_LABELS' own default below rather than being silently dropped.
const TYPE_ORDER = ["Album", "EP", "Single", "Broadcast", "Other"];
const TYPE_LABELS: Record<string, string> = {
  Album: "Albums", EP: "EPs", Single: "Singles", Broadcast: "Broadcasts", Other: "Other",
};
const UNCLASSIFIED_LABEL = "Not yet classified";

interface DiscographyGroup {
  label: string;
  works: PersonWork[];
}

// Groups preserve the discography's own order within themselves (already
// year, then sort_title, then id from the API) -- this only partitions,
// it never re-sorts. A work with no primary_type yet (created before
// migration 0028, or from a source with no MusicBrainz answer) lands in its
// own trailing group rather than being guessed into "Other".
function groupDiscography(discography: readonly PersonWork[]): DiscographyGroup[] {
  const byType = new Map<string, PersonWork[]>();
  for (const work of discography) {
    const key = work.primary_type ?? "";
    const list = byType.get(key);
    if (list) list.push(work);
    else byType.set(key, [work]);
  }

  const groups: DiscographyGroup[] = [];
  for (const type of TYPE_ORDER) {
    const works = byType.get(type);
    if (works) {
      groups.push({ label: TYPE_LABELS[type] ?? type, works });
      byType.delete(type);
    }
  }
  const unclassified = byType.get("");
  byType.delete("");
  for (const [type, works] of byType) {
    groups.push({ label: type, works });
  }
  if (unclassified) groups.push({ label: UNCLASSIFIED_LABEL, works: unclassified });
  return groups;
}

/** A provider id as a link to that provider's page of the person. */
const PROFILE_LINKS: Record<string, (id: string) => string> = {
  tmdb: (id) => `https://www.themoviedb.org/person/${id}`,
  imdb: (id) => `https://www.imdb.com/name/${id}/`,
  "musicbrainz:artist": (id) => `https://musicbrainz.org/artist/${id}`,
  wikidata: (id) => `https://www.wikidata.org/wiki/${id}`,
};
const PROFILE_LABELS: Record<string, string> = { tmdb: "TMDB", imdb: "IMDb", "musicbrainz:artist": "MusicBrainz", wikidata: "Wikidata" };

/** One page per person (ADR-32): who they are, then what they did in each library the viewer may read, each library's
 *  works shown the way that library shows them, and what the providers also know them for. */
export function PersonPage() {
  const params = useParams({ strict: false }) as { id?: string; personId?: string; categoryId?: string };
  const id = Number(params.personId ?? params.id);
  const from = params.categoryId ? Number(params.categoryId) : undefined;
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const types = useMediaTypes();
  const administrator = useIsAdministrator();

  const detail = useQuery({
    queryKey: ["person", id],
    enabled: Number.isFinite(id),
    queryFn: () => ok(api.GET("/people/{id}", { params: { path: { id } } })),
  });
  const libraries = useLibraries();
  const searchMissing = usePersonAction(id, "/people/{id}/search");
  const refresh = usePersonAction(id, "/people/{id}/refresh");
  const monitorAll = usePersonAction(id, "/people/{id}/monitor-all");
  const [editing, setEditing] = useState(false);
  const [merging, setMerging] = useState(false);
  const remove = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/people/{id}/remove", { params: { path: { id } } }));
    },
    onSuccess: () => {
      invalidatePeopleLists(queryClient);
      void navigate({ to: "/people" });
    },
  });

  if (!Number.isFinite(id)) return <Message>No person id in the address.</Message>;
  if (detail.isLoading) return <Message>Loading…</Message>;
  if (detail.error !== null || !detail.data) return <Message>This person could not be found.</Message>;

  const person = detail.data;
  const typeOf = (library: Library | undefined) => types.find((type) => type.key === library?.media_type);
  const libraryOf = (categoryId: number) => libraries.data?.find((library) => library.id === categoryId);
  const sectionIds = [...new Set(person.libraries.map((place) => place.category_id))].sort((a, b) =>
    a === from ? -1 : b === from ? 1 : (libraries.data?.findIndex((l) => l.id === a) ?? 0) - (libraries.data?.findIndex((l) => l.id === b) ?? 0),
  );
  // The artist's own verbs, for a person who leads works in a library whose type has a lead role.
  const leads = person.libraries.some((place) => typeOf(libraryOf(place.category_id))?.roles.some((role) => role.lead && role.key === place.role));
  const canRequestLead = person.libraries.some((place) => {
    const library = libraryOf(place.category_id);
    return typeOf(library)?.roles.some((role) => role.lead && role.key === place.role) && (library?.permission === "request" || library?.permission === "manage");
  });
  const facts = [
    person.disambiguation,
    person.country,
    [person.born && `Born ${person.born}`, person.died && `Died ${person.died}`].filter(Boolean).join(" · "),
  ].filter((fact): fact is string => Boolean(fact));
  const links = Object.entries(person.ids).filter(([provider]) => PROFILE_LINKS[provider]);
  const details = detailRows(person);
  const tagLibrary = from ?? sectionIds[0];
  const tagLevel = tagLibrary === undefined ? undefined : libraryOf(tagLibrary)?.levels.find((level) => level.entity === "work")?.key;
  const profiled = [...new Map(sectionIds.map((categoryId) => [libraryOf(categoryId)?.media_type ?? "", categoryId])).values()];

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        {from !== undefined && (
          <Link
            to="/library/$categoryId/$level"
            params={{ categoryId: String(from), level: libraryOf(from)?.levels.find((level) => level.entity === "person")?.key ?? "people" }}
            className="text-xs text-ink-muted hover:text-ink"
          >
            ← {typeOf(libraryOf(from))?.lead_people.many || "People"}
          </Link>
        )}
        <header className="flex items-start gap-5">
          <div className="flex shrink-0 flex-col items-center gap-2">
            {person.image_url ? (
              <img src={remoteImage(person.image_url, "small")} alt="" className="size-40 rounded-full border border-border-subtle object-cover" />
            ) : (
              <CoverArt subject="people" workId={person.id} title={person.name} size="full" className="size-40 shrink-0 rounded-full object-cover" />
            )}
            {leads && <ChooseImage subject="people" id={person.id} title={person.name} />}
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-1.5 pt-1">
            <div className="flex items-start justify-between gap-4">
              <h1 className="text-xl font-semibold">{person.name}</h1>
              {administrator && (
                <span className="flex shrink-0 gap-2">
                  <Button onClick={() => setEditing(true)}>Edit</Button>
                  <Button onClick={() => setMerging(true)}>Merge into…</Button>
                </span>
              )}
              {administrator && leads && (
                <button
                  type="button"
                  disabled={remove.isPending}
                  onClick={() => {
                    if (
                      window.confirm(
                        `Stop following ${person.name} and remove them from the library?\n\n` +
                          "Their works and any files stay exactly where they are — this only removes the person.",
                      )
                    ) {
                      remove.mutate();
                    }
                  }}
                  className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted enabled:hover:text-danger"
                >
                  Remove
                </button>
              )}
            </div>
            {facts.length > 0 && <p className="text-sm text-ink-muted">{facts.join(" · ")}</p>}
            {person.aliases.length > 0 && <p className="text-xs text-ink-muted">Also known as {person.aliases.join(", ")}</p>}
            {person.genres.length > 0 && (
              <GenreChips genres={person.genres} tags={person.genre_tags ?? []} categoryId={tagLibrary === undefined ? undefined : String(tagLibrary)} level={tagLevel} />
            )}
            {details.length > 0 && (
              <dl className="grid max-w-xl grid-cols-[max-content_1fr] gap-x-5 gap-y-1 pt-2 text-sm" aria-label="Details">
                {details.map((fact) => (
                  <Fragment key={fact.label}>
                    <dt className="text-ink-muted">{fact.label}</dt>
                    <dd>{fact.value}</dd>
                  </Fragment>
                ))}
              </dl>
            )}
            {person.biography && <p className="max-w-3xl whitespace-pre-line pt-1 text-sm leading-relaxed text-ink-muted">{person.biography}</p>}
            {person.links && person.links.length > 0 && (
              <div className="flex flex-wrap gap-3 pt-1 text-xs">
                {person.links.map((link) => (
                  <a key={link.url} href={link.url} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                    {link.label}
                  </a>
                ))}
              </div>
            )}
            {links.length > 0 && (
              <div className="flex gap-3 pt-1 text-xs">
                {links.map(([provider, external]) => (
                  <a key={provider} href={PROFILE_LINKS[provider]!(external)} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                    {PROFILE_LABELS[provider]}
                  </a>
                ))}
              </div>
            )}
          </div>
        </header>

        {person.images && person.images.length > 1 && (
          <section aria-label="Pictures" className="flex gap-2 overflow-x-auto pb-1">
            {person.images.map((url) => (
              <img key={url} src={remoteImage(url, "small")} alt="" loading="lazy" className="h-48 w-32 shrink-0 rounded-md border border-border-subtle object-cover" />
            ))}
          </section>
        )}

        {leads && (
          <div className="flex flex-wrap items-center gap-2 text-xs text-ink-muted">
            {canRequestLead && (
              <button type="button" disabled={searchMissing.isPending} onClick={() => searchMissing.mutate()} className="rounded-md border border-border-subtle px-2 py-1 hover:text-ink disabled:opacity-50">
                Search all missing
              </button>
            )}
            <button
              type="button"
              disabled={refresh.isPending}
              onClick={() => refresh.mutate()}
              title="Fetch this artist's details, picture and albums again, then rescan their folders"
              className="rounded-md border border-border-subtle px-2 py-1 hover:text-ink disabled:opacity-50"
            >
              Refresh
            </button>
            {administrator && (
              <button
                type="button"
                disabled={monitorAll.isPending}
                onClick={() => {
                  if (window.confirm(`Monitor every album of ${person.name}?`)) monitorAll.mutate();
                }}
                className="rounded-md border border-border-subtle px-2 py-1 hover:text-ink disabled:opacity-50"
              >
                Monitor all
              </button>
            )}
            {searchMissing.isSuccess && <span>Searching in the background.</span>}
            {refresh.isSuccess && <span>Refreshing in the background.</span>}
            {monitorAll.isSuccess && <span>Monitored.</span>}
            {(searchMissing.isError || refresh.isError || monitorAll.isError) && <span className="text-danger">That could not be started.</span>}
          </div>
        )}

        <FollowPanel subject="person" subjectId={person.id} libraryIds={sectionIds} />

        {sectionIds.map((categoryId) => {
          const library = libraryOf(categoryId);
          return (
            <LibrarySection
              key={categoryId}
              name={library?.name ?? "Library"}
              type={typeOf(library)}
              credits={person.credits.filter((credit) => credit.category_id === categoryId)}
            />
          );
        })}
        {sectionIds.length === 0 && <p className="text-sm text-ink-muted">No work in your libraries credits them yet.</p>}
        {editing && <EditPerson person={person} onClose={() => setEditing(false)} />}
        {merging && <MergePerson person={person} onClose={() => setMerging(false)} />}

        {profiled.length > 0 && <KnownForSection personId={person.id} categoryIds={profiled} />}
      </div>
    </div>
  );
}

/** One of the artist verbs on a person; the page is read again when it succeeds. */
/** A person's genres; a performer's tags run to dozens, so the first few show and the rest wait behind a button. */
const VISIBLE_GENRES = 12;
function GenreChips({ genres, tags, categoryId, level }: { genres: readonly string[]; tags: readonly { id: number; name: string }[]; categoryId?: string; level?: string }) {
  const [all, setAll] = useState(false);
  const shown = all ? genres : genres.slice(0, VISIBLE_GENRES);
  const chip = "rounded-full border border-border-subtle px-2 py-0.5 text-xs text-ink-muted";
  return (
    <div className="flex flex-wrap items-center gap-1.5 pt-1">
      {shown.map((genre) => {
        const tag = tags.find((entry) => entry.name === genre);
        return tag && categoryId && level ? (
          <Link
            key={genre}
            to="/library/$categoryId/$level"
            params={{ categoryId, level }}
            search={{ tags: String(tag.id) }}
            title={`Everything in this library tagged ${genre}`}
            className={`${chip} hover:border-accent hover:text-accent`}
          >
            {genre}
          </Link>
        ) : (
          <span key={genre} className={chip}>
            {genre}
          </span>
        );
      })}
      {genres.length > VISIBLE_GENRES && (
        <button type="button" onClick={() => setAll(!all)} className="text-xs text-accent hover:underline">
          {all ? "Show fewer" : `Show all ${genres.length}`}
        </button>
      )}
    </div>
  );
}

type PersonDetail = components["schemas"]["PersonDetail"];

/** The overview's rows: gender, nationality, status and the attributes first, then the provider's other facts. */
function detailRows(person: PersonDetail): { label: string; value: string }[] {
  const attributes = person.attributes ?? {};
  const mine: { label: string; value: string | undefined }[] = [
    { label: "Gender", value: person.gender },
    { label: "Nationality", value: attributes.nationality },
    { label: "Status", value: attributes.status && attributes.status[0]!.toUpperCase() + attributes.status.slice(1) },
    { label: "Height", value: attributes.height_cm ? `${attributes.height_cm} cm` : undefined },
    { label: "Weight", value: attributes.weight_kg ? `${attributes.weight_kg} kg` : undefined },
    { label: "Measurements", value: attributes.measurements },
    { label: "Hair colour", value: attributes.hair_color },
    { label: "Ethnicity", value: attributes.ethnicity },
    { label: "Tattoos", value: attributes.tattoos },
    { label: "Piercings", value: attributes.piercings },
  ];
  const rows = mine.filter((row): row is { label: string; value: string } => Boolean(row.value));
  const taken = new Set(rows.map((row) => row.label.toLowerCase()));
  return [...rows, ...(person.facts ?? []).filter((fact) => !taken.has(fact.label.toLowerCase()))];
}

const GENDERS = ["Female", "Male", "Trans woman", "Trans man", "Non-binary"];

/** Rename a person and set their gender and attributes; what is saved here stays through a provider's refresh. */
function EditPerson({ person, onClose }: { person: PersonDetail; onClose: () => void }) {
  const queryClient = useQueryClient();
  const given = person.attributes ?? {};
  const [name, setName] = useState(person.name);
  const [gender, setGender] = useState(person.gender ?? "");
  const [text, setText] = useState({
    nationality: given.nationality ?? "",
    height: given.height_cm ? String(given.height_cm) : "",
    weight: given.weight_kg ? String(given.weight_kg) : "",
    measurements: given.measurements ?? "",
    hair: given.hair_color ?? "",
    ethnicity: given.ethnicity ?? "",
    status: given.status ?? "",
    tattoos: given.tattoos ?? "",
    piercings: given.piercings ?? "",
  });
  const set = (key: keyof typeof text) => (event: { target: { value: string } }) => setText({ ...text, [key]: event.target.value });
  const save = useMutation({
    mutationFn: () =>
      ok(
        api.PATCH("/people/{id}", {
          params: { path: { id: person.id } },
          body: {
            name: name.trim(),
            gender: gender.trim() || undefined,
            attributes: {
              nationality: text.nationality.trim() || undefined,
              height_cm: Number(text.height) > 0 ? Number(text.height) : undefined,
              weight_kg: Number(text.weight) > 0 ? Number(text.weight) : undefined,
              measurements: text.measurements.trim() || undefined,
              hair_color: text.hair.trim() || undefined,
              ethnicity: text.ethnicity.trim() || undefined,
              status: text.status === "" ? undefined : (text.status as "active" | "inactive" | "deceased"),
              tattoos: text.tattoos.trim() || undefined,
              piercings: text.piercings.trim() || undefined,
            },
          },
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["person", person.id] });
      invalidatePeopleLists(queryClient);
      invalidateWorkLists(queryClient);
      onClose();
    },
  });
  const field = (label: string, key: keyof typeof text, placeholder?: string) => (
    <label className={labelStyle}>
      {label}
      <input className={inputStyle} value={text[key]} onChange={set(key)} placeholder={placeholder} />
    </label>
  );
  return (
    <Dialog
      title={`Edit ${person.name}`}
      description="What you set here stays when a provider refreshes the person."
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={save.isPending} disabled={!name.trim()} onClick={() => save.mutate()}>
            Save
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-3">
        <label className={`${labelStyle} col-span-2`}>
          Name
          <input className={inputStyle} value={name} onChange={(event) => setName(event.target.value)} />
        </label>
        <label className={labelStyle}>
          Gender
          <input className={inputStyle} list="person-genders" value={gender} onChange={(event) => setGender(event.target.value)} />
          <datalist id="person-genders">
            {GENDERS.map((entry) => (
              <option key={entry} value={entry} />
            ))}
          </datalist>
        </label>
        <label className={labelStyle}>
          Status
          <select className={inputStyle} value={text.status} onChange={set("status")}>
            <option value="">Unknown</option>
            <option value="active">Active</option>
            <option value="inactive">Inactive</option>
            <option value="deceased">Deceased</option>
          </select>
        </label>
        {field("Nationality", "nationality")}
        {field("Ethnicity", "ethnicity")}
        {field("Height (cm)", "height")}
        {field("Weight (kg)", "weight")}
        {field("Measurements", "measurements", "86-61-89")}
        {field("Hair colour", "hair")}
        {field("Tattoos", "tattoos")}
        {field("Piercings", "piercings")}
        {save.isError && <p className="col-span-2 text-xs text-danger">That could not be saved.</p>}
      </div>
    </Dialog>
  );
}

/** Fold this person into another one found by name: their credits, ids and tags move over, the name becomes an alias. */
function MergePerson({ person, onClose }: { person: PersonDetail; onClose: () => void }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [target, setTarget] = useState<{ id: number; name: string }>();
  const merge = useMutation({
    mutationFn: () => ok(api.POST("/people/{id}/merge", { params: { path: { id: person.id } }, body: { into_id: target!.id } })),
    onSuccess: () => {
      invalidatePeopleLists(queryClient);
      invalidateWorkLists(queryClient);
      void queryClient.invalidateQueries({ queryKey: ["person"] });
      void navigate({ to: "/people/$id", params: { id: String(target!.id) } });
      onClose();
    },
  });
  return (
    <Dialog
      title={`Merge ${person.name} into another person`}
      description="Their works, ids, tags and follows move to the person you pick, and this name becomes an alias of theirs. Files stay where they are."
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={merge.isPending} disabled={!target} onClick={() => merge.mutate()}>
            {target ? `Merge into ${target.name}` : "Merge"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-2">
        {target && (
          <p className="text-sm">
            Merge into <strong>{target.name}</strong>
          </p>
        )}
        <PersonPicker autoFocus exclude={person.id} label="Find the person to merge into" onPick={setTarget} />
        {merge.isError && <p className="text-xs text-danger">That could not be merged.</p>}
      </div>
    </Dialog>
  );
}

function usePersonAction(id: number, path: "/people/{id}/search" | "/people/{id}/refresh" | "/people/{id}/monitor-all") {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => ok(api.POST(path, { params: { path: { id } } })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["person", id] });
      // Monitor-all changes the person's albums too.
      invalidateWorkLists(queryClient);
    },
  });
}

/** A work once however many roles the person had in it: the first credit, with every role. */
function onePerWork(credits: Credit[]): { credit: Credit; roles: string[] }[] {
  const out: { credit: Credit; roles: string[] }[] = [];
  for (const credit of credits) {
    const found = out.find((entry) => entry.credit.work.id === credit.work.id);
    if (!found) out.push({ credit, roles: [credit.role] });
    else if (!found.roles.includes(credit.role)) found.roles.push(credit.role);
  }
  return out;
}

const WORK_ORDERS: readonly (readonly [string, string])[] = [["", "Default"], ["title", "Title"], ["year", "Year"], ["status", "Availability"]];
const STATUS_RANK = ["complete", "downloading", "incomplete", "unreleased", "unverified"];

/** The works in the order the person chose, as the library's own list offers it; the default keeps the server's. */
function sortWorks<T>(items: readonly T[], workOf: (item: T) => PersonWork, sort: string, descending: boolean): T[] {
  if (sort === "") return [...items];
  const compare = (a: PersonWork, b: PersonWork): number => {
    switch (sort) {
      case "year":
        return (a.year ?? Number.MAX_SAFE_INTEGER) - (b.year ?? Number.MAX_SAFE_INTEGER);
      case "status":
        return STATUS_RANK.indexOf(a.status) - STATUS_RANK.indexOf(b.status);
      default:
        return a.sort_title.localeCompare(b.sort_title);
    }
  };
  const ordered = [...items].sort((a, b) => compare(workOf(a), workOf(b)) || workOf(a).sort_title.localeCompare(workOf(b).sort_title) || workOf(a).id - workOf(b).id);
  return descending ? ordered.reverse() : ordered;
}

/** One library's part of the person: the lead role as that library lists it (an artist's discography by release
 *  type), every other role as a wall of the works, with the role chips when there are several. */
function LibrarySection({ name, type, credits }: { name: string; type?: MediaTypeInfo; credits: Credit[] }) {
  const [role, setRole] = useState("");
  const roles = [...new Set(credits.map((credit) => credit.role))];
  const label = (key: string) => type?.roles.find((entry) => entry.key === key)?.label ?? key.replace(/_/g, " ");
  // Only a type listed by cover (albums by release type) has a discography; posters are a wall of titles.
  const lead = type?.work_image === "poster" ? undefined : type?.roles.find((entry) => entry.lead)?.key;
  const shown = role ? credits.filter((credit) => credit.role === role) : credits;
  const workCount = new Set(shown.map((credit) => credit.work.id)).size;
  const works = (workCount === 1 ? type?.work.one : type?.work.many) ?? (workCount === 1 ? "work" : "works");
  const { sort = "", dir = "" } = useSearch({ strict: false }) as { sort?: string; dir?: string };
  const navigate = useNavigate();
  const order = (next: { sort?: string; dir?: string }) =>
    void navigate({ to: ".", replace: true, search: (previous: Record<string, unknown>) => ({ ...previous, ...next }) });
  const leadWorks = sortWorks(shown.filter((credit) => credit.role === lead).map((credit) => credit.work), (work) => work, sort, dir === "desc");
  const others = sortWorks(onePerWork(shown.filter((credit) => credit.role !== lead)), (entry) => entry.credit.work, sort, dir === "desc");
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="mr-2 text-sm font-semibold">
          {name} <span className="font-normal text-ink-muted">· {workCount} {works.toLowerCase()}</span>
        </h2>
        {roles.length > 1 &&
          ["", ...roles].map((key) => (
            <button
              key={key || "all"}
              type="button"
              aria-pressed={key === role}
              onClick={() => setRole(key)}
              className={`rounded-full border px-3 py-1 text-xs ${key === role ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"}`}
            >
              {key ? label(key) : "All"}
            </button>
          ))}
        {workCount > 1 && (
          <span className="ml-auto flex items-center gap-1 text-xs">
            <select aria-label="Order" value={sort} onChange={(event) => order({ sort: event.target.value || undefined })} className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink">
              {WORK_ORDERS.map(([key, text]) => (
                <option key={key} value={key}>
                  {key === "" ? "Order: default" : text}
                </option>
              ))}
            </select>
            {sort !== "" && (
              <button type="button" aria-label={dir === "desc" ? "Descending" : "Ascending"} onClick={() => order({ dir: dir === "desc" ? undefined : "desc" })} className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink">
                {dir === "desc" ? "↓" : "↑"}
              </button>
            )}
          </span>
        )}
      </div>
      {groupDiscography(leadWorks).map((group) => (
        <div key={group.label} className="flex flex-col gap-1.5">
          <h3 className="text-xs font-medium uppercase tracking-wide text-ink-muted">
            {group.label} ({group.works.length})
          </h3>
          <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
            {group.works.map((work) => (
              <AlbumRow key={work.id} work={work} />
            ))}
          </ul>
        </div>
      ))}
      {others.length > 0 && (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
          {others.map(({ credit, roles: held }) => (
            <CreditTile key={credit.work.id} credit={credit} label={held.map(label).join(" · ")} />
          ))}
        </div>
      )}
    </section>
  );
}

function PosterImage({ url, kind, workId, title }: { url?: string; kind: string; workId?: number; title: string }) {
  const Icon = mediaTypeIcon(kind);
  const [failed, setFailed] = useState(false);
  return (
    <span className="block aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-surface">
      {url && !failed ? (
        <img src={remoteImage(url, "small")} alt="" loading="lazy" onError={() => setFailed(true)} className="size-full object-cover" />
      ) : workId ? (
        <CoverArt workId={workId} title={title} size="small" className="size-full object-cover" />
      ) : (
        <span className="flex size-full items-center justify-center text-ink-muted">
          <Icon aria-hidden className="size-8" />
        </span>
      )}
    </span>
  );
}

export function CreditTile({ credit, label }: { credit: Credit; label: string }) {
  const parts = (credit.parts ?? []).map((part) => part.replace(/:/g, "").toUpperCase());
  return (
    <Link {...workLink(credit.category_id, credit.work.id)} className="group flex flex-col gap-1 focus-visible:outline-2 focus-visible:outline-accent">
      <PosterImage url={credit.work.poster_url} kind={credit.work.media_type} workId={credit.work.poster_url ? undefined : credit.work.id} title={credit.work.title} />
      <span className="truncate text-sm font-medium">
        {credit.work.title}
        {credit.work.year ? <span className="font-normal text-ink-muted"> ({credit.work.year})</span> : null}
      </span>
      <span className="truncate text-xs text-ink-muted">{[label, credit.character].filter(Boolean).join(" · ")}</span>
      {parts.length > 0 && (
        <span className="truncate text-xs text-ink-muted" title={parts.join(", ")}>
          {parts.length <= 3 ? parts.join(", ") : `${parts.length} episodes`}
        </span>
      )}
    </Link>
  );
}

const KIND_LABEL: Record<string, string> = { tv: "Series", movie: "Movies", album: "Albums", book: "Books", audiobook: "Audiobooks" };

/** What the providers know the person for beyond the library, asked once per media type the person appears in and
 * shown once: the TV and film types both answer TMDB's whole list. */
function KnownForSection({ personId, categoryIds }: { personId: number; categoryIds: number[] }) {
  const queryClient = useQueryClient();
  const answers = useQueries({
    queries: categoryIds.map((categoryId) => ({
      queryKey: ["known-for", personId, categoryId],
      retry: false,
      queryFn: async () => {
        const { data, error } = await api.GET("/people/{id}/known-for", { params: { path: { id: personId }, query: { category: categoryId } } });
        if (error) throw error;
        // Asking the provider fills in a biography and dates the page did not have yet.
        void queryClient.invalidateQueries({ queryKey: ["person", personId] });
        return data;
      },
    })),
  });
  const seen = new Set<string>();
  const titles: KnownFor[] = [];
  for (const answer of answers) {
    for (const title of answer.data ?? []) {
      const key = `${title.provider}-${title.kind}-${title.external_id}`;
      if (!seen.has(key)) {
        seen.add(key);
        titles.push(title);
      }
    }
  }
  const [adding, setAdding] = useState<KnownFor>();
  if (titles.length === 0) return null;
  const kinds = [...new Set(titles.map((title) => title.kind))];
  const keyOf = (title: KnownFor) => `${title.provider}-${title.kind}-${title.external_id}`;
  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-sm font-semibold">Also known for</h2>
      {kinds.map((kind) => (
        <div key={kind} className="flex flex-col gap-2">
          <h3 className="text-xs font-medium text-ink-muted">{KIND_LABEL[kind] ?? kind}</h3>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
            {titles
              .filter((title) => title.kind === kind)
              .map((title) => (
                <KnownForTile
                  key={keyOf(title)}
                  title={title}
                  adding={adding !== undefined && keyOf(adding) === keyOf(title)}
                  onAdd={() => setAdding(adding && keyOf(adding) === keyOf(title) ? undefined : title)}
                />
              ))}
          </div>
          {adding?.kind === kind && adding.add_library_id && (
            <AddToLibrary
              key={keyOf(adding)}
              candidate={{ provider: adding.provider, external_id: adding.external_id, title: adding.title, kind: adding.kind }}
              categoryId={String(adding.add_library_id)}
              onDone={() => setAdding(undefined)}
            />
          )}
        </div>
      ))}
    </section>
  );
}

/** A title the library lacks is added from here to the library that holds its kind; with none, it links out. */
function KnownForTile({ title, adding, onAdd }: { title: KnownFor; adding: boolean; onAdd: () => void }) {
  const mediaType = title.kind === "album" ? "music" : title.kind;
  const caption = (
    <>
      <PosterImage url={title.poster_url} kind={mediaType} title={title.title} />
      <span className="truncate text-sm font-medium">
        {title.title}
        {title.year ? <span className="font-normal text-ink-muted"> ({title.year})</span> : null}
      </span>
      <span className="truncate text-xs text-ink-muted">{title.role}</span>
    </>
  );
  if (!title.add_library_id) {
    const outbound =
      title.provider === "openlibrary"
        ? `https://openlibrary.org/works/${title.external_id}`
        : title.kind === "album"
          ? `https://musicbrainz.org/release-group/${title.external_id}`
          : `https://www.themoviedb.org/${title.kind}/${title.external_id}`;
    return (
      <a href={outbound} target="_blank" rel="noreferrer" className="flex flex-col gap-1 focus-visible:outline-2 focus-visible:outline-accent">
        {caption}
        <span className="text-xs text-ink-muted">No {mediaTypeName(mediaType)} library to add it to</span>
      </a>
    );
  }
  return (
    <div className="flex flex-col gap-1">
      {caption}
      <button
        type="button"
        aria-expanded={adding}
        onClick={onAdd}
        className="w-fit rounded-md border border-border-subtle px-2 py-0.5 text-xs text-accent hover:border-accent"
      >
        Add to Solarr
      </button>
    </div>
  );
}

// One discography row, expandable in place to its own tracklist (lazy --
// fetched only once expanded, and only once per row) rather than always
// sending the user to WorkDetail just to see what is on an album.
function AlbumRow({ work }: { work: PersonWork }) {
  const [expanded, setExpanded] = useState(false);

  const tracks = useAllParts(work.id, undefined, expanded);

  return (
    <li className="flex flex-col text-sm">
      <div className="flex items-center gap-3 px-3 py-2">
        <button
          type="button"
          aria-expanded={expanded}
          aria-label={expanded ? "Collapse tracklist" : "Expand tracklist"}
          onClick={() => setExpanded((current) => !current)}
          className="shrink-0 text-ink-muted hover:text-ink"
        >
          {expanded ? "▾" : "▸"}
        </button>
        <CoverArt
          workId={work.id}
          title={work.title}
          size="thumb"
          className="h-8 w-8 shrink-0 rounded object-cover"
        />
        <Link to="/works/$id" params={{ id: String(work.id) }} className="min-w-0 flex-1 truncate hover:underline">
          {work.title}
        </Link>
        {work.year != null && <span className="shrink-0 text-xs text-ink-muted">{work.year}</span>}
        <AvailabilityBadge work={work} />
      </div>

      {expanded && (
        <div className="border-t border-border-subtle bg-canvas px-3 py-2 pl-11">
          {tracks.loading && <p className="text-xs text-ink-muted">Loading tracks…</p>}
          {tracks.error && <p className="text-xs text-ink-muted">Tracks could not be read.</p>}
          {!tracks.loading && !tracks.error && tracks.parts.length === 0 && (
            <p className="text-xs text-ink-muted">No tracklist yet.</p>
          )}
          {tracks.parts.length > 0 && (
            <ul className="flex flex-col gap-1">
              {tracks.parts.map((part) => (
                <li key={part.id} className="flex items-center gap-3 text-xs">
                  <span className="w-10 shrink-0 text-right font-mono tabular-nums text-ink-muted">
                    {formatTrackNumber(part.media_ref, part.sort_key, discCountOf(tracks.parts))}
                  </span>
                  <span className="min-w-0 flex-1 truncate">{part.title ?? part.media_ref}</span>
                  {part.runtime_ms != null && (
                    <span className="shrink-0 font-mono text-ink-muted">{formatDuration(part.runtime_ms)}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </li>
  );
}

// Mirrors WorkDetail's own WantedBadge/OnDiskBadge, simplified to what
// PersonWork actually carries (no required/present track counts at this
// level -- that is what expanding the row is for).
function AvailabilityBadge({ work }: { work: PersonWork }) {
  const counts =
    work.required_count != null && work.required_count > 0
      ? `${work.present_count ?? 0}/${work.required_count}`
      : null;
  return (
    <span className="flex shrink-0 items-center gap-2">
      {counts && <span className="font-mono text-xs tabular-nums text-ink-muted">{counts}</span>}
      <span
        className={`rounded-full border border-current px-2 py-0.5 text-xs ${albumStatusText(work.status)}`}
      >
        {albumStatusLabel(work.status)}
      </span>
    </span>
  );
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.round(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
