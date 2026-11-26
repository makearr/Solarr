import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { EyeOff } from "lucide-react";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { invalidateWorkLists } from "@/lib/resources";
import { useIsAdministrator } from "@/lib/useAdministrator";

import { Button } from "./Button";
import { Chip } from "./Chip";

/** An ignore from a list names no kind: it holds for every kind the provider id could be of. */
const IGNORE_ANY_KIND = "any";

/** What an ignore names: the item as its provider knows it, and the library it was found for. */
export interface Ignorable {
  categoryId: number;
  provider: string;
  externalId: string;
  title: string;
}

/** Ignore an item the library lacks: it is not offered as missing again, in any list. Administrators may ignore it for everyone. */
export function IgnoreButton({ item }: { item: Ignorable }) {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const ignore = useMutation({
    mutationFn: (everyone: boolean) =>
      ok(api.POST("/ignores", { body: { category_id: item.categoryId, kind: IGNORE_ANY_KIND, provider: item.provider, external_id: item.externalId, title: item.title, everyone } })),
    onSuccess: () => {
      invalidateWorkLists(queryClient);
      void queryClient.invalidateQueries({ queryKey: ["discover-recommendations"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-dismissals"] });
    },
  });
  return (
    <span className="inline-flex items-center gap-1">
      <Button icon={<EyeOff aria-hidden className="size-3.5" />} busy={ignore.isPending} onClick={() => ignore.mutate(false)} title={t("ignore.help")}>
        {t("ignore.ignore")}
      </Button>
      {administrator && (
        <Button busy={ignore.isPending} onClick={() => ignore.mutate(true)} title={t("ignore.everyoneHelp")}>
          {t("ignore.everyone")}
        </Button>
      )}
    </span>
  );
}

/** What an ignore of a held work or person names: the library it is shown in and its own id. */
export interface HeldIgnorable {
  categoryId: number;
  subject: "work" | "person";
  id: number;
}

/** Ignore an item the library holds: it is left out of every list that offers things as missing (a work is left unmonitored). Administrators may ignore it for everyone. */
export function IgnoreHeldButton({ item }: { item: HeldIgnorable }) {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const [everyone, setEveryone] = useState<boolean>();
  const change = useMutation({
    mutationFn: ({ ignored, forAll }: { ignored: boolean; forAll: boolean }) => {
      const body = { category_id: item.categoryId, subject: item.subject, id: item.id, everyone: forAll };
      return ok(ignored ? api.POST("/ignores/held", { body }) : api.DELETE("/ignores/held", { body }));
    },
    onSuccess: (_, { ignored, forAll }) => {
      setEveryone(ignored ? forAll : undefined);
      invalidateWorkLists(queryClient, item.id);
      void queryClient.invalidateQueries({ queryKey: ["discover-recommendations"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-dismissals"] });
    },
  });
  if (everyone !== undefined) {
    return (
      <span className="inline-flex items-center gap-1">
        <Chip>{t("ignore.ignored")}</Chip>
        <Button busy={change.isPending} onClick={() => change.mutate({ ignored: false, forAll: everyone })}>
          {t("ignore.stop")}
        </Button>
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1">
      <Button icon={<EyeOff aria-hidden className="size-3.5" />} busy={change.isPending} onClick={() => change.mutate({ ignored: true, forAll: false })} title={t("ignore.helpHeld")}>
        {t("ignore.ignore")}
      </Button>
      {administrator && (
        <Button busy={change.isPending} onClick={() => change.mutate({ ignored: true, forAll: true })} title={t("ignore.everyoneHelp")}>
          {t("ignore.everyone")}
        </Button>
      )}
    </span>
  );
}
