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

/** The form a library is added with, on the Libraries page and in the category settings. */
export function AddLibraryForm() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [mediaType, setMediaType] = useState<MediaType>("music");
  const [kind, setKind] = useState("");
  const mediaTypes = useMediaTypes();

  const create = useMutation({
    mutationFn: () => ok(api.POST("/categories", {
      body: { name: name.trim() || mediaTypeName(mediaType), media_type: mediaType, kind: kind || undefined },
    })),
    onSuccess: () => {
      setName("");
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
      <label className="flex min-w-32 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          autoComplete="off"
          placeholder={mediaTypeName(mediaType)}
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className={labelStyle}>
        Media type
        <select
          className={inputStyle}
          value={mediaType}
          onChange={(event) => {
            setMediaType(event.target.value as MediaType);
            setKind("");
          }}
        >
          {mediaTypes.map((type) => (
            <option key={type.key} value={type.key}>
              {type.label}
            </option>
          ))}
        </select>
      </label>
      <KindSelect mediaType={mediaType} value={kind} onChange={setKind} />
      <Button
        variant="primary"
        type="submit"
        disabled={create.isPending}
      >
        <Plus aria-hidden className="size-4" />
        {create.isPending ? "Adding…" : "Add library"}
      </Button>
      {create.isError && (
        <p className="w-full text-xs text-danger">That library could not be created. Check the name is not empty.</p>
      )}
    </form>
  );
}
