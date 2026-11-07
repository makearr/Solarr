import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import { Button } from "@/ui/Button";

import { Card } from "./SettingsCard";
import { BackupRetentionEditor } from "./Scheduler";

/** Settings > Database, expert tier: where Solarr's own folders spend space, how many backups are kept, and the
 *  maintenance that can also be run by hand. */
export function DatabaseSettingsPage() {
  const tier = useTier();
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">Database</h1>
        </header>
        {isTierVisible(tier, "expert") ? (
          <DatabaseSettings />
        ) : (
          <p className="text-sm text-ink-muted">Database maintenance is an expert-tier setting. Raise your own tier on the User settings page to see it.</p>
        )}
      </div>
    </div>
  );
}

function DatabaseSettings() {
  return (
    <section className="flex flex-col gap-4">
      <UsageCard />
      <BackupsCard />
      <MaintenanceCard />
    </section>
  );
}

function UsageCard() {
  const usage = useQuery({ queryKey: ["database"], queryFn: () => ok(api.GET("/database")) });
  if (usage.error !== null || !usage.data) return null;
  const rows = [
    ["Pictures of library items (mediacover)", usage.data.media_cover_bytes],
    ["Video previews", usage.data.previews_bytes],
    ["Cache", usage.data.cache_bytes],
    ["Backups", usage.data.backups_bytes],
  ] as const;
  return (
    <Card title="Disk use" help="What Solarr's own folders hold, apart from the databases themselves (see Cache for those).">
      <dl className="grid grid-cols-[1fr_auto] gap-x-6 gap-y-1 text-sm">
        {rows.map(([label, bytes]) => (
          <div key={label} className="contents">
            <dt className="text-ink-muted">{label}</dt>
            <dd className="text-right tabular-nums">{formatBytes(bytes)}</dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}

function BackupsCard() {
  const queryClient = useQueryClient();
  const usage = useQuery({ queryKey: ["database"], queryFn: () => ok(api.GET("/database")) });
  const [confirming, setConfirming] = useState(false);
  const clear = useMutation({
    mutationFn: () => ok(api.POST("/database/backups/clear", {})),
    onSuccess: () => {
      setConfirming(false);
      return queryClient.invalidateQueries({ queryKey: ["database"] });
    },
  });
  const latest = usage.data?.latest_backup_at;
  return (
    <Card
      title="Backups"
      help="Each backup is one compressed bundle of the databases and config.yml, checked before it is kept. The Backup task makes them; this decides how many stay."
    >
      <BackupRetentionEditor />
      <p className="text-xs text-ink-muted">
        {usage.data ? `${usage.data.backup_count} on disk` : ""}
        {latest ? `, the newest from ${new Date(latest).toLocaleString()}.` : "."}
      </p>
      {!confirming ? (
        <Button variant="secondary" onClick={() => setConfirming(true)} disabled={!usage.data || usage.data.backup_count < 2}>
          Clear old backups
        </Button>
      ) : (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="text-ink-muted">Checks that the newest backup is valid, then removes all older ones.</span>
          <Button variant="danger" onClick={() => clear.mutate()} disabled={clear.isPending}>
            {clear.isPending ? "Checking…" : "Check and clear"}
          </Button>
          <Button variant="secondary" onClick={() => setConfirming(false)}>
            Cancel
          </Button>
        </div>
      )}
      {clear.isError && <p className="text-xs text-danger">The newest backup did not pass its check, so nothing was removed.</p>}
      {clear.isSuccess && <p className="text-xs text-ink-muted">Removed {clear.data.removed}; kept {clear.data.kept}.</p>}
    </Card>
  );
}

const TASKS = [
  { key: "database_maintenance", label: "Maintain the databases now", help: "Integrity check, then vacuum and truncate the log. Writes to a database wait while it runs." },
  { key: "backup", label: "Back up now", help: "Takes a new verified bundle." },
  { key: "backup_cleanup", label: "Apply the keep rule now", help: "Removes the backups beyond the rule above." },
] as const;

function MaintenanceCard() {
  const queryClient = useQueryClient();
  const run = useMutation({
    mutationFn: (key: string) => ok(api.POST("/schedules/{key}/run", { params: { path: { key } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["database"] }),
  });
  return (
    <Card title="Maintenance" help="The scheduled tasks, started by hand. Each also appears under Scheduler.">
      <ul className="flex flex-col gap-2">
        {TASKS.map((task) => (
          <li key={task.key} className="flex flex-wrap items-center gap-3">
            <Button variant="secondary" onClick={() => run.mutate(task.key)} disabled={run.isPending}>
              {task.label}
            </Button>
            <span className="text-xs text-ink-muted">{task.help}</span>
          </li>
        ))}
      </ul>
      {run.isError && <p className="text-xs text-danger">That task could not be started.</p>}
      {run.isSuccess && <p className="text-xs text-ink-muted">Started; watch Scheduler or Activity for the result.</p>}
    </Card>
  );
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exponent;
  return `${value >= 10 || exponent === 0 ? Math.round(value) : value.toFixed(1)} ${units[exponent]}`;
}
