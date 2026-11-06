import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { TARGET_FAMILIES } from "@/lib/targetDisplay";
import { Button, RemoveButton } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

import { AUDIO_CODECS, ENCODER_PRESETS, ENCODER_TUNES, Num, Pick, RESOLUTIONS, VIDEO_CODECS } from "./QualityTargets";
import { Card } from "./SettingsCard";

type TargetProfile = components["schemas"]["TargetProfile"];

const smallButton = "rounded-md border border-border-subtle px-2.5 py-1 text-xs text-ink-muted hover:text-ink";
const AUDIO_MEDIA = new Set(["music", "spoken"]);

function ProfileForm({ initial, onDone, onCancel }: { initial?: TargetProfile; onDone: () => void; onCancel: () => void }) {
  const [profile, setProfile] = useState<TargetProfile>(initial ?? { name: "", media_types: ["video"] });
  const targets = useQuery({ queryKey: ["qualityTargets"], queryFn: () => ok(api.GET("/qualitytargets")) });
  const set = (change: Partial<TargetProfile>) => setProfile({ ...profile, ...change });
  const audio = profile.media_types.every((type) => AUDIO_MEDIA.has(type));
  const recommendable = [...new Set((targets.data ?? []).filter((target) => profile.media_types.includes(target.media_type ?? "")).map((target) => target.name ?? ""))];
  const save = useMutation({
    mutationFn: async () => {
      const { error } = initial?.id
        ? await api.PUT("/targetprofiles/{id}", { params: { path: { id: initial.id } }, body: profile })
        : await api.POST("/targetprofiles", { body: profile });
      if (error) throw error;
    },
    onSuccess: onDone,
  });
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-canvas p-3 text-xs">
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-ink-muted">
          Name
          <input value={profile.name} onChange={(event) => set({ name: event.target.value })} className={`${inputStyle} w-48`} />
        </label>
        <fieldset className="flex flex-wrap gap-3 text-ink-muted">
          <legend className="mb-1">For</legend>
          {TARGET_FAMILIES.map((type) => (
            <label key={type.key} className="flex items-center gap-1">
              <input
                type="checkbox"
                checked={profile.media_types.includes(type.key)}
                onChange={(event) => set({ media_types: event.target.checked ? [...profile.media_types, type.key] : profile.media_types.filter((key) => key !== type.key) })}
              />
              {type.label}
            </label>
          ))}
        </fieldset>
      </div>
      <label className="flex flex-col gap-1 text-ink-muted">
        Description
        <input value={profile.description ?? ""} onChange={(event) => set({ description: event.target.value })} className={inputStyle} />
      </label>
      <div className="flex flex-wrap items-end gap-3">
        {!audio && (
          <>
            <Pick label="Resolution" value={profile.resolution} options={RESOLUTIONS} onChange={(resolution) => set({ resolution })} />
            <Pick label="Video codec" value={profile.video_codec} options={VIDEO_CODECS} onChange={(video_codec) => set({ video_codec })} />
            <Num label="Frame rate cap" value={profile.max_frame_rate || undefined} width="w-24" onChange={(max_frame_rate) => set({ max_frame_rate })} />
            <Num label="CRF" value={profile.crf || undefined} step={0.5} width="w-20" onChange={(crf) => set({ crf })} />
            <Pick label="Encoder preset" value={profile.encoder_preset} options={ENCODER_PRESETS} onChange={(encoder_preset) => set({ encoder_preset })} />
            <Pick label="Encoder tune" value={profile.encoder_tune} options={ENCODER_TUNES} onChange={(encoder_tune) => set({ encoder_tune })} />
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={profile.deinterlace ?? false} onChange={(event) => set({ deinterlace: event.target.checked })} />
              Deinterlace
            </label>
            <Num label="Audio bitrate (kbit/s)" value={profile.audio_bitrate_kbps} onChange={(audio_bitrate_kbps) => set({ audio_bitrate_kbps })} />
          </>
        )}
        {audio && (
          <>
            <Pick label="Codec" value={profile.audio_codec} options={AUDIO_CODECS} onChange={(audio_codec) => set({ audio_codec })} />
            <Pick label="Channels" value={profile.audio_channels} options={["1.0", "2.0"]} onChange={(audio_channels) => set({ audio_channels })} />
            <Num label="Bitrate (kbit/s)" value={profile.bitrate_kbps} onChange={(bitrate_kbps) => set({ bitrate_kbps })} />
            <Num label="Sample rate (Hz)" value={profile.sample_rate_hz} width="w-28" onChange={(sample_rate_hz) => set({ sample_rate_hz })} />
            <Num label="Bit depth" value={profile.bit_depth} width="w-20" onChange={(bit_depth) => set({ bit_depth })} />
          </>
        )}
        <Pick
          label="Recommended target"
          value={profile.recommended_target}
          options={recommendable}
          onChange={(recommended_target) => set({ recommended_target })}
          any="none"
        />
      </div>
      <div className="flex items-center gap-2">
        <Button variant="primary" type="button" disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        <button type="button" onClick={onCancel} className={smallButton}>
          Cancel
        </button>
        {save.isError && <span className="whitespace-pre-wrap text-danger">{(save.error as { detail?: string }).detail ?? "That profile could not be saved."}</span>}
      </div>
    </div>
  );
}

/** Content profiles: a kind of material with the settings that suit it, offered when a target quality is made. */
export function TargetProfilesSection() {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<number | "new" | null>(null);
  const profiles = useQuery({ queryKey: ["targetProfiles"], queryFn: () => ok(api.GET("/targetprofiles")) });
  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/targetprofiles/{id}", { params: { path: { id } } }));
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["targetProfiles"] });
      void queryClient.invalidateQueries({ queryKey: ["qualityTargets"] });
    },
  });
  const done = () => {
    setEditing(null);
    void queryClient.invalidateQueries({ queryKey: ["targetProfiles"] });
  };
  return (
    <Card
      title="Content profiles"
      help="What kind of material a target quality is for: anime, sports, a CD rip. Each carries recommended settings and the shipped target that fits it best; picking one on a target fills them in, and you can change every number afterwards."
      toggle={
        <button type="button" onClick={() => setEditing("new")} className="flex shrink-0 items-center gap-1 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink">
          <Plus aria-hidden className="size-4" />
          New profile
        </button>
      }
    >
      {remove.isError && <p className="text-xs text-danger">{(remove.error as { detail?: string }).detail ?? "That profile could not be removed."}</p>}
      {editing === "new" && <ProfileForm onDone={done} onCancel={() => setEditing(null)} />}
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {(profiles.data ?? []).map((profile) =>
          editing === profile.id ? (
            <li key={profile.id} className="p-3">
              <ProfileForm initial={profile} onDone={done} onCancel={() => setEditing(null)} />
            </li>
          ) : (
            <li key={profile.id} className="flex items-center gap-3 px-3 py-2.5">
              <span className="min-w-0 flex-1">
                <span className="flex items-baseline gap-2 text-sm font-medium">
                  {profile.name}
                  <span className="text-xs font-normal text-ink-muted">{profile.media_types.map((key) => TARGET_FAMILIES.find((family) => family.key === key)?.label ?? key).join(", ")}</span>
                </span>
                <span className="block text-xs text-ink-muted">{profile.description}</span>
                {profile.recommended_target && <span className="block truncate text-xs text-ink-muted">Recommends {profile.recommended_target}</span>}
              </span>
              <button type="button" onClick={() => setEditing(profile.id ?? null)} className={smallButton}>
                Edit
              </button>
              <RemoveButton aria-label={`Remove content profile ${profile.name}`} disabled={remove.isPending} onClick={() => profile.id && remove.mutate(profile.id)} />
            </li>
          ),
        )}
      </ul>
    </Card>
  );
}
