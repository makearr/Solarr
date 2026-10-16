import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { EmptyState } from "@/ui/EmptyState";
import { PageHeader } from "@/ui/PageHeader";

type Franchise = components["schemas"]["Franchise"];

/** A movie library's collections, as Radarr's: the franchises its films belong to, monitored as a whole. */
export function FranchiseIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const list = useQuery({
    queryKey: ["franchises", categoryId],
    queryFn: async () => {
      const { data, error } = await api.GET("/franchises", { params: { query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const monitor = useMutation({
    mutationFn: async (franchise: Franchise) => {
      const { data, error } = await api.POST("/franchises/{id}/monitor", {
        params: { path: { id: franchise.id } },
        body: { category_id: Number(categoryId), monitored: !franchise.monitored },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
      void queryClient.invalidateQueries({ queryKey: ["works"] });
    },
  });
  if (list.isLoading) return <p className="p-6 text-sm text-ink-muted">{t("work.loading")}</p>;
  if (!list.data) return <p className="p-6 text-sm text-danger">{t("franchise.failed")}</p>;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-4xl flex-col gap-4 p-4">
        <PageHeader title={t("franchise.title")} subtitle={t("franchise.help")} />
        {list.data.franchises.length === 0 && <EmptyState title={t("franchise.none")} />}
        {list.data.franchises.map((franchise) => (
          <section key={franchise.id} className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
            <div className="flex flex-wrap items-center gap-3">
              <h2 className="font-medium">{franchise.name}</h2>
              <span className="text-xs text-ink-muted">
                {franchise.total
                  ? t("franchise.held", { held: franchise.members.length, total: franchise.total })
                  : t("franchise.heldUnknown", { held: franchise.members.length })}
              </span>
              {franchise.monitored && <Chip tone="ok">{t("franchise.monitored")}</Chip>}
              {administrator && (
                <Button className="ml-auto" busy={monitor.isPending && monitor.variables?.id === franchise.id} onClick={() => monitor.mutate(franchise)}>
                  {franchise.monitored ? t("franchise.monitored") : t("franchise.monitor")}
                </Button>
              )}
            </div>
            {monitor.isSuccess && monitor.variables?.id === franchise.id && (
              <p className="text-xs text-ink-muted">
                {monitor.data?.added ? t("franchise.added", { count: monitor.data.added }) : franchise.monitored ? "" : t("franchise.noKey")}
              </p>
            )}
            <ul className="flex flex-col divide-y divide-border-subtle text-sm">
              {franchise.members.map((member) => (
                <li key={member.work_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5">
                  <Link
                    to="/library/$categoryId/works/$workId/$tab"
                    params={{ categoryId, workId: String(member.work_id), tab: "overview" }}
                    className="hover:underline"
                  >
                    {member.title}
                  </Link>
                  {member.year ? <span className="text-xs text-ink-muted">{member.year}</span> : null}
                  <span className={`ml-auto text-xs ${member.has_file ? "text-ok" : member.monitored ? "text-danger" : "text-ink-muted"}`}>
                    {member.has_file ? t("franchise.onDisk") : member.monitored ? t("franchise.missing") : t("franchise.unmonitored")}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </div>
  );
}
