import { useMutation, useQueryClient } from "@tanstack/react-query";
import { EyeOff } from "lucide-react";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { invalidateWorkLists } from "@/lib/resources";
import { useIsAdministrator } from "@/lib/useAdministrator";

import { Button } from "./Button";

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
