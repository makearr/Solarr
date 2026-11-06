import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { Button } from "@/ui/Button";

import { LanguagePicker } from "./LanguagePicker";

/** The languages a new library prefers, best first. Asked once in the setup wizard; a library's own language can differ. */
export function DefaultLanguagesEditor({ onSaved, saveLabel = "Save" }: { onSaved?: () => void; saveLabel?: string }) {
  const queryClient = useQueryClient();
  const current = useQuery({ queryKey: ["defaultLanguages"], queryFn: () => ok(api.GET("/defaultlanguages")) });
  const [languages, setLanguages] = useState<string[]>([]);
  useEffect(() => {
    if (current.data) setLanguages(current.data.languages.length > 0 ? current.data.languages : ["en"]);
  }, [current.data]);
  const save = useMutation({
    mutationFn: () => ok(api.PUT("/defaultlanguages", { body: { languages } })),
    onSuccess: (data) => {
      queryClient.setQueryData(["defaultLanguages"], data);
      onSaved?.();
    },
  });
  const changed = JSON.stringify(languages) !== JSON.stringify(current.data?.languages ?? []);
  return (
    <div className="flex flex-col gap-3">
      <LanguagePicker
        label="Preferred audio languages, best first"
        help="Each library you add starts with these. An anime library puts Japanese first, since the originals are Japanese. Each library can be changed afterwards."
        value={languages}
        onChange={setLanguages}
      />
      <div className="flex items-center gap-2">
        <Button variant="primary" type="button" disabled={save.isPending || languages.length === 0 || (!changed && !onSaved)} onClick={() => save.mutate()}>
          {save.isPending ? "Saving…" : saveLabel}
        </Button>
        {save.isError && <span className="text-xs text-danger">{(save.error as { detail?: string }).detail ?? "That could not be saved."}</span>}
      </div>
    </div>
  );
}
