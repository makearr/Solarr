import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";

import { KindSelect } from "./LibraryKindSelect";

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
  return {
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
    },
    forgetName: () => setName(undefined),
    create: () => {
      if (!mediaType) throw new Error("no media type to create a library of");
      const name = (typedName ?? typeName).trim() || typeName;
      return ok(api.POST("/categories", { body: { name, media_type: mediaType, kind: kind || undefined } }));
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
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border-subtle bg-surface p-3"
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
