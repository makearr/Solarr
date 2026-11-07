import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";

import { Card, Field } from "./SettingsCard";
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
      <PicturesCard />
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

type ImageSettings = components["schemas"]["ImageSettings"];

const numberStyle =
  "w-full rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

/** How big and how compressed the stored pictures of library items and the video previews are, and a button to make the
 *  ones already stored again at those figures. */
function PicturesCard() {
  const queryClient = useQueryClient();
  const images = useQuery({
    queryKey: ["database-images"],
    queryFn: () => ok(api.GET("/database/images")),
    refetchInterval: (query) => (query.state.data?.recompress?.running ? 2000 : false),
  });
  const [draft, setDraft] = useState<ImageSettings | null>(null);
  const save = useMutation({
    mutationFn: (body: ImageSettings) => ok(api.PUT("/database/images", { body })),
    onSuccess: () => {
      setDraft(null);
      return queryClient.invalidateQueries({ queryKey: ["database-images"] });
    },
  });
  const recompress = useMutation({
    mutationFn: () => ok(api.POST("/database/images/recompress", {})),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["database-images"] }),
  });
  if (images.error !== null || !images.data) return null;
  const current = draft ?? images.data;
  const dirty = draft !== null;
  const run = images.data.recompress;
  const change = (patch: Partial<ImageSettings>) => setDraft({ ...current, ...patch });
  const number = (value: number, set: (next: number) => void, min: number, max: number) => (
    <input type="number" min={min} max={max} value={value} disabled={save.isPending} onChange={(event) => set(Number(event.target.value))} className={numberStyle} />
  );
  return (
    <Card
      title="Pictures"
      help="The longest edge and the compression of the stored pictures of library items (WebP) and of video previews (JPEG). New pictures use the figures at once; Recompress makes the stored ones again, and a picture that would not come out smaller is left alone."
    >
      <div className="flex flex-wrap items-start gap-4">
        <Field label="Cover size (px)" help="Largest side, 400 to 4000." className="w-40">
          {number(current.cover_max_edge, (cover_max_edge) => change({ cover_max_edge }), 400, 4000)}
        </Field>
        <Field label="Cover quality" help="30 to 95." className="w-32">
          {number(current.cover_quality, (cover_quality) => change({ cover_quality }), 30, 95)}
        </Field>
        <Field label="Preview size (px)" help="Frames are read 1280 wide, so more changes nothing." className="w-40">
          {number(current.preview_max_edge, (preview_max_edge) => change({ preview_max_edge }), 320, 4000)}
        </Field>
        <Field label="Preview quality" help="30 to 95." className="w-32">
          {number(current.preview_quality, (preview_quality) => change({ preview_quality }), 30, 95)}
        </Field>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="primary" onClick={() => save.mutate(current)} disabled={save.isPending || !dirty}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        <Button variant="secondary" onClick={() => recompress.mutate()} disabled={recompress.isPending || run?.running === true || dirty}>
          {run?.running ? "Recompressing…" : "Recompress stored pictures"}
        </Button>
        {dirty && <span className="text-xs text-ink-muted">Save first; Recompress uses the saved figures.</span>}
      </div>
      {run && (
        <p className="text-xs text-ink-muted">
          {run.running ? `${run.done} of ${run.total} pictures` : `Last run: ${run.done} pictures`}; {formatBytes(run.saved_bytes)} saved
          {run.failed > 0 ? `, ${run.failed} could not be read` : ""}.
        </p>
      )}
      {(save.isError || recompress.isError) && <p className="text-xs text-danger">That was refused: check the figures, or a recompress is already running.</p>}
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
