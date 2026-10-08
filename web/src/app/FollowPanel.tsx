import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BellRing } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { remoteImage } from "@/lib/remoteImage";

type Follow = components["schemas"]["Follow"];
type FollowRequest = components["schemas"]["FollowRequest"];
type Library = components["schemas"]["Library"];
type Subject = "person" | "organisation";

/** "Add their new works for me": a follow of a person or organisation into each library it can go to. */
export function FollowPanel({ subject, subjectId, libraryIds }: { subject: Subject; subjectId: number; libraryIds: readonly number[] }) {
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });
  const follows = useQuery({
    queryKey: ["follows", subject, subjectId],
    queryFn: async () => {
      const { data, error } =
        subject === "person"
          ? await api.GET("/people/{id}/follow", { params: { path: { id: subjectId } } })
          : await api.GET("/organisations/{id}/follow", { params: { path: { id: subjectId } } });
      if (error) throw error;
      return data;
    },
  });
  const mediaTypes = useMediaTypes();
  const followed = follows.data ?? [];
  const ids = [...new Set([...libraryIds, ...followed.map((follow) => follow.category_id)])];
  const shown = (libraries.data ?? []).filter(
    (library) =>
      ids.includes(library.id) &&
      (library.permission === "request" || library.permission === "manage") &&
      followableIn(mediaTypes.find((type) => type.key === library.media_type), subject),
  );
  if (shown.length === 0) return null;
  return (
    <div className="flex flex-col gap-2 text-xs">
      {shown.map((library) => (
        <FollowRow
          key={library.id}
          subject={subject}
          subjectId={subjectId}
          library={library}
          mediaType={mediaTypes.find((type) => type.key === library.media_type)!}
          follow={followed.find((follow) => follow.category_id === library.id)}
          many={shown.length > 1}
        />
      ))}
    </div>
  );
}

/** A type can follow a person when it declares a followable role, and an organisation when it declares organisations. */
function followableIn(type: MediaTypeInfo | undefined, subject: Subject): boolean {
  if (!type) return false;
  return subject === "person" ? type.roles.some((role) => role.followable) : type.organisation.many !== "";
}

function FollowRow({
  subject,
  subjectId,
  library,
  mediaType,
  follow,
  many,
}: {
  subject: Subject;
  subjectId: number;
  library: Library;
  mediaType: MediaTypeInfo;
  follow?: Follow;
  many: boolean;
}) {
  const queryClient = useQueryClient();
  const roles = subject === "person" ? mediaType.roles.filter((role) => role.followable) : [];
  const defaults = Object.fromEntries(mediaType.follow_options.map((option) => [option.key, option.default]));
  const [chosen, setChosen] = useState<string[]>(follow?.roles ?? []);
  const [options, setOptions] = useState<Record<string, string[]>>({ ...defaults, ...(follow?.options ?? {}) });
  // Only what the user set is sent: an option left alone keeps the library's own default where the type has one.
  const [touched, setTouched] = useState<string[]>(Object.keys(follow?.options ?? {}));
  const [back, setBack] = useState(follow?.back_catalogue ?? false);
  const [open, setOpen] = useState(false);
  const request: FollowRequest = {
    category_id: library.id,
    roles: chosen,
    options: Object.fromEntries(Object.entries(options).filter(([key]) => touched.includes(key))),
    back_catalogue: back,
  };
  const where = many ? ` in ${library.name}` : "";

  const preview = useQuery({
    queryKey: ["follow-preview", subject, subjectId, request],
    enabled: open,
    retry: false,
    queryFn: async () => {
      const { data, error } =
        subject === "person"
          ? await api.POST("/people/{id}/follow/preview", { params: { path: { id: subjectId } }, body: request })
          : await api.POST("/organisations/{id}/follow/preview", { params: { path: { id: subjectId } }, body: request });
      if (error) throw error;
      return data;
    },
  });
  const changed = () => {
    void queryClient.invalidateQueries({ queryKey: ["follows", subject, subjectId] });
    void queryClient.invalidateQueries({ queryKey: [subject === "person" ? "person" : "organisation", subjectId] });
  };
  const save = useMutation({
    mutationFn: async (search: boolean) => {
      const body = { ...request, search };
      const { error } =
        subject === "person"
          ? await api.PUT("/people/{id}/follow", { params: { path: { id: subjectId } }, body })
          : await api.PUT("/organisations/{id}/follow", { params: { path: { id: subjectId } }, body });
      if (error) throw error;
    },
    onSuccess: () => {
      changed();
      setOpen(false);
    },
  });
  const stop = useMutation({
    mutationFn: async () => {
      const params = { path: { id: subjectId }, query: { category_id: library.id } };
      const { error } =
        subject === "person" ? await api.DELETE("/people/{id}/follow", { params }) : await api.DELETE("/organisations/{id}/follow", { params });
      if (error) throw error;
    },
    onSuccess: changed,
  });
  const works = mediaType.work.many.toLowerCase();
  const toggleRole = (role: string) => setChosen((current) => (current.includes(role) ? current.filter((entry) => entry !== role) : [...current, role]));
  const setOption = (key: string, values: string[]) => {
    setOptions((current) => ({ ...current, [key]: values }));
    setTouched((current) => (current.includes(key) ? current : [...current, key]));
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <BellRing aria-hidden className={`size-4 ${follow ? "text-accent" : "text-ink-muted"}`} />
        {follow ? (
          <span>
            Following{where}
            {follow.roles.length > 0 ? ` as ${follow.roles.map((role) => labelOf(mediaType, role)).join(", ")}` : ""}: new {works} are added
            automatically.
          </span>
        ) : (
          <span className="text-ink-muted">
            Not following{where}: new {works} are not added for you.
          </span>
        )}
        <button type="button" onClick={() => setOpen(!open)} className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink">
          {follow ? "Change" : "Follow"}
        </button>
        {follow && (
          <button
            type="button"
            disabled={stop.isPending}
            onClick={() => stop.mutate()}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink disabled:opacity-50"
          >
            Unfollow
          </button>
        )}
      </div>
      {open && (
        <div className="flex max-w-xl flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
          {roles.length > 1 && (
            <fieldset className="flex flex-col gap-1">
              <legend className="mb-1 text-ink-muted">Follow them as</legend>
              {roles.map((role) => (
                <label key={role.key} className="flex items-center gap-2">
                  <input type="checkbox" checked={chosen.includes(role.key)} onChange={() => toggleRole(role.key)} />
                  {role.label}
                </label>
              ))}
              <span className="text-[0.6875rem] text-ink-muted">Nothing ticked means any role.</span>
            </fieldset>
          )}
          {mediaType.follow_options.map((option) =>
            option.many ? (
              <fieldset key={option.key} className="flex flex-col gap-1">
                <legend className="mb-1 text-ink-muted">{option.label}</legend>
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                  {option.choices.map((choice) => {
                    const values = options[option.key] ?? [];
                    return (
                      <label key={choice.key} className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          checked={values.includes(choice.key)}
                          onChange={() =>
                            setOption(option.key, values.includes(choice.key) ? values.filter((value) => value !== choice.key) : [...values, choice.key])
                          }
                        />
                        {choice.label}
                      </label>
                    );
                  })}
                </div>
              </fieldset>
            ) : (
              <label key={option.key} className="flex flex-col gap-1">
                <span className="text-ink-muted">{option.label}</span>
                <select
                  value={(options[option.key] ?? [])[0] ?? ""}
                  onChange={(event) => setOption(option.key, [event.target.value])}
                  className="max-w-xs rounded-md border border-border-subtle bg-canvas px-2 py-1"
                >
                  {option.choices.map((choice) => (
                    <option key={choice.key} value={choice.key}>
                      {choice.label}
                    </option>
                  ))}
                </select>
              </label>
            ),
          )}
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={back} onChange={(event) => setBack(event.target.checked)} />
            Also add older {works}, not only new ones
          </label>
          <div className="flex flex-col gap-1.5" aria-live="polite">
            <span className="text-ink-muted">What this would add right now</span>
            {preview.isLoading && <span className="text-ink-muted">Looking…</span>}
            {preview.isError && <span className="text-ink-muted">{problemOf(preview.error) ?? "The preview is not available."}</span>}
            {preview.data && preview.data.would_add.length === 0 && (
              <span className="text-ink-muted">Nothing new at the moment; {works} that appear later are added as they do.</span>
            )}
            {preview.data && preview.data.would_add.length > 0 && (
              <ul className="flex max-h-64 flex-col gap-1 overflow-y-auto">
                {preview.data.would_add.map((candidate) => (
                  <li key={`${candidate.provider}:${candidate.external_id}`} className="flex items-center gap-2">
                    {candidate.poster_url && (
                      <span className="block h-9 w-6 shrink-0 overflow-hidden rounded bg-canvas">
                        <img src={remoteImage(candidate.poster_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" />
                      </span>
                    )}
                    <span className="min-w-0 flex-1 truncate">
                      {candidate.title}
                      {candidate.year ? <span className="text-ink-muted"> ({candidate.year})</span> : null}
                    </span>
                    {candidate.role && roles.length > 1 && <span className="text-ink-muted">{labelOf(mediaType, candidate.role)}</span>}
                  </li>
                ))}
              </ul>
            )}
            {preview.data && (preview.data.more > 0 || preview.data.held > 0) && (
              <span className="text-[0.6875rem] text-ink-muted">
                {[
                  preview.data.more > 0 && `${preview.data.more} more follow in later runs (at most ${preview.data.limit} at a time)`,
                  preview.data.held > 0 && `${preview.data.held} already in your library or excluded`,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              disabled={save.isPending}
              onClick={() => save.mutate(false)}
              className="rounded-md bg-accent px-3 py-1 text-accent-ink disabled:opacity-50"
            >
              {follow ? "Save" : "Follow"}
            </button>
            {!follow && (
              <button
                type="button"
                disabled={save.isPending}
                onClick={() => save.mutate(true)}
                className="rounded-md border border-border-subtle px-3 py-1 text-ink-muted hover:text-ink disabled:opacity-50"
              >
                Follow and search
              </button>
            )}
            {save.isError && <span className="text-danger">{problemOf(save.error) ?? "That could not be saved."}</span>}
          </div>
        </div>
      )}
    </div>
  );
}

function labelOf(type: MediaTypeInfo, role: string): string {
  return type.roles.find((entry) => entry.key === role)?.label ?? role.replace(/_/g, " ");
}

/** The server's own explanation of a refusal, when it gave one. */
function problemOf(error: unknown): string | undefined {
  if (error && typeof error === "object" && "detail" in error && typeof error.detail === "string") return error.detail;
  return undefined;
}
