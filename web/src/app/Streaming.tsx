import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

import { Card, SaveBar, SettingRow, Switch } from "./SettingsCard";

type StreamingReport = components["schemas"]["StreamingReport"];
type StreamingSettings = components["schemas"]["StreamingSettings"];

const HARDWARE: Record<StreamingSettings["hardware"], string> = {
  auto: "Automatic",
  software: "Processor only",
  vaapi: "VAAPI (Intel and AMD)",
  qsv: "Intel Quick Sync",
  nvenc: "NVIDIA NVENC",
};

/** The built-in player's server-side conversion: whether it is on, which encoder it uses (detected, or chosen here)
 *  and how much it may take. */
export function Streaming() {
  const queryClient = useQueryClient();
  const report = useQuery({ queryKey: ["streaming"], queryFn: () => ok(api.GET("/streaming")) });
  const store = (data: StreamingReport) => queryClient.setQueryData(["streaming"], data);

  const save = useMutation({
    mutationFn: (body: StreamingSettings) => ok(api.PATCH("/streaming", { body })),
    onSuccess: store,
  });
  const detect = useMutation({ mutationFn: () => ok(api.POST("/streaming/detect")), onSuccess: store });

  if (!report.data) return null;
  return (
    <StreamingForm
      key={JSON.stringify(report.data.settings)}
      report={report.data}
      busy={save.isPending}
      error={save.isError ? "That was refused; check the values." : null}
      onSave={(body) => save.mutate(body)}
      detecting={detect.isPending}
      onDetect={() => detect.mutate()}
    />
  );
}

function StreamingForm({
  report,
  busy,
  error,
  onSave,
  detecting,
  onDetect,
}: {
  report: StreamingReport;
  busy: boolean;
  error: string | null;
  onSave: (body: StreamingSettings) => void;
  detecting: boolean;
  onDetect: () => void;
}) {
  const saved = report.settings;
  const [draft, setDraft] = useState<StreamingSettings>(saved);
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  const set = (change: Partial<StreamingSettings>) => setDraft({ ...draft, ...change });
  const effective = report.effective;

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        onSave({ ...draft, device: draft.device?.trim() || undefined });
      }}
    >
      <Card
        title="Converting for the player"
        help="Files the browser cannot play as they are (most MKV, HEVC, FLAC in Safari, WMA) are converted by ffmpeg while they stream. Off, only files the browser plays itself can be previewed."
      >
        <SettingRow label="Convert on the server" help={report.ffmpeg_available ? undefined : "ffmpeg is not installed, so nothing can be converted."}>
          <Switch label="Convert on the server" checked={draft.transcode_enabled} disabled={busy} onChange={(value) => set({ transcode_enabled: value })} />
        </SettingRow>
        <SettingRow label="Conversions at once" help="A further one is refused until one ends.">
          <input type="number" min={1} max={32} className={`${inputStyle} w-24`} value={draft.max_sessions} disabled={busy}
            onChange={(event) => set({ max_sessions: Number(event.target.value) })} />
        </SettingRow>
        <SettingRow label="Largest picture height" help="Taller video is scaled down while it is converted.">
          <input type="number" min={144} max={4320} className={`${inputStyle} w-24`} value={draft.max_height} disabled={busy}
            onChange={(event) => set({ max_height: Number(event.target.value) })} />
        </SettingRow>
        <SettingRow label="Video bitrate (kbit/s)">
          <input type="number" min={200} max={100000} step={500} className={`${inputStyle} w-24`} value={draft.video_kbps} disabled={busy}
            onChange={(event) => set({ video_kbps: Number(event.target.value) })} />
        </SettingRow>
      </Card>

      <Card
        title="Graphics hardware"
        help="Encoding on a GPU leaves the processor free. Automatic uses the best encoder found working at start; choose one here to override it."
      >
        <SettingRow label="Encoder">
          <select className={inputStyle} value={draft.hardware} disabled={busy}
            onChange={(event) => set({ hardware: event.target.value as StreamingSettings["hardware"] })}>
            {Object.entries(HARDWARE).map(([value, label]) => (
              <option key={value} value={value}>{label}</option>
            ))}
          </select>
        </SettingRow>
        <SettingRow label="Render device" help="Empty takes the one found, such as /dev/dri/renderD128.">
          <input type="text" placeholder="/dev/dri/renderD128" className={`${inputStyle} w-56 font-mono`} value={draft.device ?? ""} disabled={busy}
            onChange={(event) => set({ device: event.target.value })} />
        </SettingRow>
        <div className="flex flex-col gap-1 border-t border-border-subtle pt-3 text-sm" aria-live="polite">
          <p>
            In use: <strong>{effective.accel === "software" ? "the processor" : `${effective.accel.toUpperCase()}${effective.device ? ` on ${effective.device}` : ""}`}</strong>
            {report.active_sessions > 0 && ` · ${report.active_sessions} converting now`}
          </p>
          {effective.note && <p className="text-xs text-warn">{effective.note}</p>}
          {report.detected.length === 0 ? (
            <p className="text-xs text-ink-muted">No graphics encoder was found. Inside Docker, pass the device through (for Intel and AMD: <code>--device /dev/dri</code>).</p>
          ) : (
            <ul className="text-xs text-ink-muted">
              {report.detected.map((found) => (
                <li key={`${found.accel}-${found.device ?? ""}`}>
                  {found.name ?? "GPU"} · {found.accel.toUpperCase()}{found.device ? ` · ${found.device}` : ""} ·{" "}
                  <span className={found.working ? "text-ok" : "text-danger"}>{found.working ? "works" : `does not work${found.detail ? `: ${found.detail}` : ""}`}</span>
                </li>
              ))}
            </ul>
          )}
          <div>
            <Button busy={detecting} busyLabel="Looking…" onClick={onDetect}>Detect again</Button>
          </div>
        </div>
      </Card>
      <SaveBar dirty={dirty} busy={busy} onSave={() => onSave({ ...draft, device: draft.device?.trim() || undefined })} onUndo={() => setDraft(saved)} error={error} />
    </form>
  );
}
