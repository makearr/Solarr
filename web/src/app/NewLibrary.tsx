import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle, addFormStyle } from "@/ui/form";

import { KindSelect } from "./LibraryKindSelect";
import { TargetChoiceField, useTargetChoice } from "./LibraryTargetChoice";

type MediaType = components["schemas"]["MediaType"];

/** A library about to be created: the first type this build brings until one is picked, named after its type until
 *  someone types a name (2026-09-19: "the default library name should always be the category, for example Music"). */
export function useNewLibrary() {
  const mediaTypes = useMediaTypes();
  const [picked, setPicked] = useState<MediaType>();
  const [typedName, setName] = useState<string>();
  const [kind, setKind] = useState("");
  const mediaType = picked ?? mediaTypes[0]?.key;
  const typeName = mediaType ? mediaTypeName(mediaType) : "";
  const kinds = mediaTypes.find((type) => type.key === mediaType)?.kinds ?? [];
  const effectiveKind = kind || kinds[0]?.key || "";
  // Anime is one library for its series and its films, so movies start ticked for it unless unticked.
  const [asked, setAsked] = useState<Record<string, boolean>>({});
  const offeredTypes = mediaTypes.filter((type) => type.key !== mediaType);
  const isAlso = (key: string) => asked[key] ?? (key === "movie" && mediaType === "tv" && effectiveKind === "anime");
  const alsoTypes = offeredTypes.filter((type) => isAlso(type.key)).map((type) => type.key);
  const target = useTargetChoice(mediaType);
  return {
    offeredTypes,
    alsoTypes,
    target,
    toggleAlso: (key: string, on: boolean) => setAsked((current) => ({ ...current, [key]: on })),
    mediaTypes,
    mediaType,
    typeName,
    name: typedName ?? typeName,
    kind,
    setName,
    setKind,
    setMediaType: (value: MediaType) => {
      setPicked(value);
      setKind("");
      setAsked({});
    },
    forgetName: () => setName(undefined),
    create: () => {
      if (!mediaType) throw new Error("no media type to create a library of");
      const name = (typedName ?? typeName).trim() || typeName;
      return ok(api.POST("/categories", { body: { name, media_type: mediaType, kind: kind || undefined } })).then(async (created) => {
        for (const key of alsoTypes) await ok(api.POST("/categories/{id}/types", { params: { path: { id: created.id } }, body: { media_type: key as MediaType } }));
        await target.apply(created.id);
        return created;
      });
    },
  };
}

export type NewLibrary = ReturnType<typeof useNewLibrary>;

/** The name, media type and kind of a library about to be created, as one row of inline fields. */
export function NewLibraryFields({ library }: { library: NewLibrary }) {
  return (
    <>
      <label className={`${labelStyle} min-w-32 flex-1`}>
        Library name
        <input
          className={inputStyle}
          value={library.name}
          placeholder={library.typeName}
          autoComplete="off"
          onChange={(event) => library.setName(event.target.value)}
        />
      </label>
      <label className={labelStyle}>
        Media type
        <select
          className={inputStyle}
          value={library.mediaType ?? ""}
          onChange={(event) => library.setMediaType(event.target.value as MediaType)}
        >
          {library.mediaTypes.map((type) => (
            <option key={type.key} value={type.key}>
              {type.label}
            </option>
          ))}
        </select>
      </label>
      {library.mediaType && <KindSelect mediaType={library.mediaType} value={library.kind} onChange={library.setKind} />}
      <TargetChoiceField choice={library.target} />
      {library.offeredTypes.length > 0 && (
        <fieldset className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
          <legend className="sr-only">Also holds</legend>
          <span className="text-ink-muted">Also holds</span>
          {library.offeredTypes.map((type) => (
            <label key={type.key} className="flex items-center gap-1">
              <input
                type="checkbox"
                checked={library.alsoTypes.includes(type.key)}
                onChange={(event) => library.toggleAlso(type.key, event.target.checked)}
              />
              {type.work.many}
            </label>
          ))}
        </fieldset>
      )}
    </>
  );
}

/** The form a library is added with, on the Libraries page and in the category settings. */
export function AddLibraryForm() {
  const queryClient = useQueryClient();
  const library = useNewLibrary();
  const create = useMutation({
    mutationFn: library.create,
    onSuccess: () => {
      library.forgetName();
      void queryClient.invalidateQueries();
    },
  });

  return (
    <form
      className={addFormStyle}
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <NewLibraryFields library={library} />
      <Button variant="primary" type="submit" disabled={create.isPending || !library.mediaType}>
        <Plus aria-hidden className="size-4" />
        {create.isPending ? "Adding…" : "Add library"}
      </Button>
      {create.isError && (
        <p className="w-full text-xs text-danger">That library could not be created. Check the name is not empty.</p>
      )}
    </form>
  );
}
