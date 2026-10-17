import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Film } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { Button } from "@/ui/Button";

import { CoverArt } from "../CoverArt";
import { heldLabel } from "../Franchises";
import { AddToLibrary } from "./AddWork";

type Franchise = components["schemas"]["Franchise"];
type Member = components["schemas"]["FranchiseMember"];

/** "Part of the … collection" under a work: the other works of each collection it is in, those the library lacks to add. */
export function WorkCollections({ workId, categoryId }: { workId: number; categoryId: string }) {
  const list = useQuery({
    queryKey: ["work-franchises", categoryId, workId],
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}/franchises", { params: { path: { id: workId }, query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data.franchises;
    },
  });
  if (!list.data?.length) return null;
  return (
    <>
      {list.data.map((franchise) => (
        <PartOf key={franchise.id} franchise={franchise} workId={workId} categoryId={categoryId} />
      ))}
    </>
  );
}

function PartOf({ franchise, workId, categoryId }: { franchise: Franchise; workId: number; categoryId: string }) {
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState<Member>();
  const detail = useQuery({
    queryKey: ["franchise", categoryId, franchise.id],
    queryFn: async () => {
      const { data, error } = await api.GET("/franchises/{id}", { params: { path: { id: franchise.id }, query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const changed = () => {
    void queryClient.invalidateQueries({ queryKey: ["franchise", categoryId, franchise.id] });
    void queryClient.invalidateQueries({ queryKey: ["work-franchises", categoryId] });
    void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
  };
  const monitor = useMutation({
    mutationFn: async (monitored: boolean) => {
      const { data, error } = await api.POST("/franchises/{id}/monitor", { params: { path: { id: franchise.id } }, body: { category_id: Number(categoryId), monitored } });
      if (error) throw error;
      return data;
    },
    onSuccess: changed,
  });
  const shown = detail.data ?? franchise;
  const others = shown.members.filter((member) => member.work_id !== workId);
  const missing = others.filter((member) => !member.work_id).length;
  if (others.length === 0) return null;
  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 className="text-sm font-semibold">
          <Link to="/library/$categoryId/collections/$franchiseId" params={{ categoryId, franchiseId: String(franchise.id) }} className="hover:underline">
            {/collection$/i.test(shown.name.trim()) ? t("collection.partOfNamed", { name: shown.name }) : t("collection.partOf", { name: shown.name })}
          </Link>
        </h2>
        <span className="text-xs text-ink-muted">{heldLabel(shown)}</span>
        {administrator && shown.kind !== "custom" && (missing > 0 || shown.monitored) && (
          <Button busy={monitor.isPending} onClick={() => monitor.mutate(!shown.monitored)} title={t("collection.monitorHelp")}>
            {shown.monitored ? t("franchise.monitored") : t("collection.addMissing", { count: missing })}
          </Button>
        )}
        {monitor.isSuccess && monitor.data.added > 0 && <span className="text-xs text-ok">{t("franchise.added", { count: monitor.data.added })}</span>}
      </div>
      <div className="flex gap-3 overflow-x-auto pb-1">
        {others.map((member) => (
          <MemberTile key={member.work_id ?? `${member.provider}:${member.external_id}`} member={member} categoryId={categoryId} onAdd={administrator ? () => setAdding(member) : undefined} />
        ))}
      </div>
      {adding?.provider && adding.external_id && (
        <AddToLibrary
          candidate={{ provider: adding.provider, external_id: adding.external_id, title: adding.title }}
          categoryId={categoryId}
          onDone={() => {
            setAdding(undefined);
            changed();
          }}
        />
      )}
    </section>
  );
}

function MemberTile({ member, categoryId, onAdd }: { member: Member; categoryId: string; onAdd?: () => void }) {
  const picture = (
    <span className="block aspect-[2/3] w-28 overflow-hidden rounded-lg border border-border-subtle bg-canvas">
      {member.poster_url ? (
        <img src={remoteImage(member.poster_url, "small")} alt="" loading="lazy" className={`size-full object-cover ${member.work_id ? "" : "opacity-60"}`} />
      ) : member.work_id ? (
        <CoverArt workId={member.work_id} title={member.title} size="small" className="size-full object-cover" />
      ) : (
        <span className="flex size-full items-center justify-center text-ink-muted">
          <Film aria-hidden className="size-8" />
        </span>
      )}
    </span>
  );
  const caption = (
    <span className="w-28 truncate text-xs font-medium" title={member.title}>
      {member.title}
      {member.year ? <span className="font-normal text-ink-muted"> ({member.year})</span> : null}
    </span>
  );
  if (member.work_id) {
    return (
      <Link
        to="/library/$categoryId/works/$workId/$tab"
        params={{ categoryId, workId: String(member.work_id), tab: "overview" }}
        className="flex shrink-0 flex-col gap-1 focus-visible:outline-2 focus-visible:outline-accent"
      >
        {picture}
        {caption}
      </Link>
    );
  }
  return (
    <div className="flex shrink-0 flex-col gap-1">
      {picture}
      {caption}
      {onAdd && member.provider && member.external_id && (
        <button type="button" onClick={onAdd} className="w-fit rounded-md border border-border-subtle px-2 py-0.5 text-xs text-accent hover:border-accent">
          {t("collection.request")}
        </button>
      )}
    </div>
  );
}
