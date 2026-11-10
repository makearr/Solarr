import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { Button } from "@/ui/Button";

import { formatBytes } from "./Queue";
import { Card } from "./SettingsCard";

type DiskReport = components["schemas"]["DiskReport"];

const RAISE_WATCHES_HELP =
  "On the host (not in the container): sudo sysctl fs.inotify.max_user_watches=<number>, and the same line without sudo in /etc/sysctl.d/99-inotify.conf to keep it after a reboot.";

/** Settings -> Disk: what Solarr found about the disks under its folders, what a probe of each costs, and the inotify
 *  budget. Expert tier: the page exists to correct a wrong guess. */
export function DiskSettingsPage() {
  const tier = useTier();
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">Disk</h1>
        </header>
        {isTierVisible(tier, "expert") ? (
          <DiskSettings />
        ) : (
          <p className="text-sm text-ink-muted">The disk layout is an expert-tier setting. Raise your own tier on the User settings page to see it.</p>
        )}
      </div>
    </div>
  );
}

function DiskSettings() {
  const queryClient = useQueryClient();
  const report = useQuery({ queryKey: ["disk"], queryFn: () => ok(api.GET("/settings/disk")) });
  const [groups, setGroups] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!report.data) return;
    setGroups(Object.fromEntries(report.data.paths.map((row) => [row.path, row.group])));
  }, [report.data]);

  const store = (data: DiskReport) => queryClient.setQueryData(["disk"], data);
  const save = useMutation({
    mutationFn: (overrides: Record<string, string>) => ok(api.PUT("/settings/disk", { body: { groups: overrides } })),
    onSuccess: store,
  });
  const measure = useMutation({ mutationFn: () => ok(api.POST("/settings/disk/measure")), onSuccess: store });

  if (report.error !== null || !report.data) return null;
  const data = report.data;

  // Only a label that differs from what was detected is a correction; the rest stay automatic.
  const typed = (row: { path: string; group: string }) => (groups[row.path] ?? row.group).trim();
  const corrections = Object.fromEntries(data.paths.filter((row) => typed(row) !== (data.paths.find((p) => p.path === row.path)?.detected_group ?? "")).map((row) => [row.path, typed(row)]));
  const dirty = JSON.stringify(corrections) !== JSON.stringify(Object.fromEntries(data.paths.filter((row) => row.overridden).map((row) => [row.path, row.group])));

  return (
    <section className="flex flex-col gap-4">
      <p className="max-w-2xl text-sm text-ink-muted">
        Paths in the same group share one disk's IO: Solarr gives them one budget, so a scan of one library does not starve an import into another.
        Detected from the container's mount table and /sys; correct a group here when the guess is wrong, for example two datasets on different pools that you know share a disk.
      </p>

      <Card title="Detected layout" help="Each folder Solarr uses, and the disk group it was placed in. Type the same group name on two rows to make them share a disk; clear a row to go back to the detected group.">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-ink-muted">
              <tr>
                <th className="py-1 pr-3 font-medium">Path</th>
                <th className="py-1 pr-3 font-medium">Filesystem</th>
                <th className="py-1 pr-3 font-medium">Space</th>
                <th className="py-1 pr-3 font-medium">Why</th>
                <th className="py-1 font-medium">Group</th>
              </tr>
            </thead>
            <tbody>
              {data.paths.map((row) => (
                <tr key={row.path} className="border-t border-border-subtle align-top">
                  <td className="py-1.5 pr-3 font-mono">{row.path}</td>
                  <td className="py-1.5 pr-3">{row.fs_type || "unknown"}{row.pool ? `, ${row.pool}` : ""}{row.disk ? `, disk ${row.disk}` : ""}</td>
                  <td className="py-1.5 pr-3 whitespace-nowrap">{row.total_bytes > 0 ? `${formatBytes(row.free_bytes)} free of ${formatBytes(row.total_bytes)}` : "-"}</td>
                  <td className="py-1.5 pr-3 text-ink-muted">{row.evidence}</td>
                  <td className="py-1.5">
                    <input
                      aria-label={`Disk group of ${row.path}`}
                      className="w-40 rounded border border-border-subtle bg-surface px-2 py-1 font-mono"
                      value={groups[row.path] ?? row.group}
                      placeholder={row.detected_group}
                      onChange={(event) => setGroups({ ...groups, [row.path]: event.target.value })}
                    />
                    {row.overridden && <span className="ml-2 text-warn">corrected</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="flex items-center gap-3">
          <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(corrections)}>
            Save corrections
          </Button>
          {save.error && <span className="text-xs text-danger">{String(save.error)}</span>}
        </div>
      </Card>

      <Card
        title="Latency"
        help="How long a directory read takes per disk group. Solarr measures only while work is waiting for that disk, so an idle drive is left to spin down; Measure now touches every disk once."
      >
        <table className="w-full text-left text-xs">
          <thead className="text-ink-muted">
            <tr>
              <th className="py-1 pr-3 font-medium">Group</th>
              <th className="py-1 pr-3 font-medium">Last</th>
              <th className="py-1 pr-3 font-medium">Average</th>
              <th className="py-1 pr-3 font-medium">Readings</th>
              <th className="py-1 font-medium">Measured</th>
            </tr>
          </thead>
          <tbody>
            {data.latencies.map((row) => (
              <tr key={row.group} className="border-t border-border-subtle">
                <td className="py-1.5 pr-3 font-mono">{row.group}</td>
                <td className="py-1.5 pr-3">{row.measured_at ? `${row.last_ms.toFixed(1)} ms` : "-"}</td>
                <td className="py-1.5 pr-3">{row.samples > 0 ? `${row.mean_ms.toFixed(1)} ms` : "-"}</td>
                <td className="py-1.5 pr-3">{row.samples}</td>
                <td className="py-1.5">{row.error ? <span className="text-danger">{row.error}</span> : row.measured_at ? new Date(row.measured_at * 1000).toLocaleString() : "never"}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <div>
          <Button disabled={measure.isPending} onClick={() => measure.mutate()}>
            Measure now
          </Button>
        </div>
      </Card>

      <Card title="Inotify watches" help="Solarr watches folders for changes so it does not have to rescan; it never takes more than half of what the host allows.">
        <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1 text-xs">
          <dt className="text-ink-muted">Host limit</dt>
          <dd title={RAISE_WATCHES_HELP}>{data.inotify.max_user_watches > 0 ? data.inotify.max_user_watches.toLocaleString() : "unreadable"}</dd>
          <dt className="text-ink-muted">Solarr's budget (50%)</dt>
          <dd>{data.inotify.budget_watches.toLocaleString()}</dd>
          <dt className="text-ink-muted">Held by Solarr</dt>
          <dd>{data.inotify.used_by_us.toLocaleString()}</dd>
        </dl>
        <p className="text-xs text-ink-muted">{RAISE_WATCHES_HELP}</p>
      </Card>
    </section>
  );
}
