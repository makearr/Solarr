import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";

import { Blocklist } from "./Blocklist";
import { Tags } from "./Tags";
import { Categories } from "./Categories";
import { MetadataSettings } from "./MetadataSettings";
import { Network } from "./Network";
import { Streaming } from "./Streaming";
import { Priority } from "./Priority";
import { UnwantedFiles } from "./UnwantedFiles";
import { useFooterItems, useShowCalendar, useShowFooter, type FooterItem } from "@/lib/interfacePreferences";
import { isTierVisible, useTier, type Tier } from "@/lib/useTier";

import { SubTabs } from "@/ui/SubTabs";

import { Card } from "./SettingsCard";

/** Settings -> General, split apart on the 2026-09-19 review: Libraries, Blocklists, Network,
 *  Metadata. Quality profiles, indexers and download clients live where they are configured. */
function SettingsPage({
  title,
  requiredTier = "normal",
  children,
}: {
  title: string;
  requiredTier?: Tier;
  children: React.ReactNode;
}) {
  const tier = useTier();
  // Reached by a link or an old bookmark: say why it is empty rather than show a bare title (2026-09-28).
  const hidden = !isTierVisible(tier, requiredTier);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <h1 className="text-base font-semibold">{title}</h1>
        {hidden ? (
          <p className="text-sm text-ink-muted">
            These settings are shown from the {requiredTier === "expert" ? "Expert" : "Advanced"} level on.
            Choose it in the menu under your name, top right.
          </p>
        ) : (
          children
        )}
      </div>
    </div>
  );
}

type SolarrMode = NonNullable<components["schemas"]["GeneralSettings"]["solarr_mode"]>;

const SOLARR_MODES: readonly { value: SolarrMode; label: string; help: string }[] = [
  { value: "normal", label: "Normal", help: "Everything Solarr is set up to do." },
  { value: "read_only", label: "Read-only libraries", help: "Nothing is written into a library folder: no imports, renames, moves or deletes. Searching and downloading go on." },
  { value: "pause_downloads", label: "Pause downloads", help: "Nothing is sent to a download client. Libraries can still be managed." },
  { value: "observe", label: "Observe only", help: "Libraries are read-only, no indexer is searched and nothing is downloaded." },
  { value: "arr_companion", label: "*Arr companion", help: "Observe only, and the monitoring of what an *Arr import matched is sent back to its *Arr." },
];

/** Switches that belong to no one page. */
export function GeneralSettings() {
  const queryClient = useQueryClient();
  const general = useQuery({ queryKey: ["general-settings"], queryFn: () => ok(api.GET("/settings/general")) });
  const change = useMutation({
    mutationFn: (body: components["schemas"]["GeneralSettings"]) => ok(api.PATCH("/settings/general", { body })),
    onSuccess: (data) => {
      queryClient.setQueryData(["general-settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["video-hashing"] });
    },
  });
  return (
    <SettingsPage title="General">
      <Card title="Mode" help="How much of the outside world Solarr touches. SOLARR_MODE in the environment sets this at every start.">
        <fieldset className="flex flex-col gap-2" disabled={!general.data || change.isPending}>
          <legend className="sr-only">Solarr mode</legend>
          {SOLARR_MODES.map((choice) => (
            <label key={choice.value} className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="solarr-mode"
                className="mt-1"
                checked={(general.data?.solarr_mode ?? "normal") === choice.value}
                onChange={() => change.mutate({ solarr_mode: choice.value })}
              />
              <span>
                {choice.label}
                <span className="block text-xs text-ink-muted">{choice.help}</span>
              </span>
            </label>
          ))}
        </fieldset>
      </Card>
      <Card title="Background work" help="Switches for work Solarr does by itself. A search you start yourself works whatever these say.">
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-1"
            disabled={!general.data || change.isPending}
            checked={general.data?.hunt_old_missing ?? true}
            onChange={(event) => change.mutate({ hunt_old_missing: event.target.checked })}
          />
          <span>
            Search for old missing items
            <span className="block text-xs text-ink-muted">The automatic search also looks for missing items released before the freshness window. Off, only new releases and upgrades are searched.</span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-1"
            disabled={!general.data || change.isPending}
            checked={general.data?.video_hashing ?? false}
            onChange={(event) => change.mutate({ video_hashing: event.target.checked })}
          />
          <span>
            Hash videos to find look-alike duplicates (perceptual hash)
            <span className="block text-xs text-ink-muted">Reads seven frames per video with ffmpeg in the background so Storage, Duplicates can match re-encodes. Off, nothing new is hashed; stored hashes keep working.</span>
          </span>
        </label>
        {change.isError && <p className="text-sm text-danger">That change was refused.</p>}
      </Card>
    </SettingsPage>
  );
}

/** Every library, and how new content is prioritised across them. */
export function LibrariesSettings() {
  return (
    <SettingsPage title="Libraries">
      <SubTabs
        tabs={[
          { key: "libraries", label: "Libraries", content: <Categories /> },
          { key: "priority", label: "Priority", content: <Priority /> },
        ]}
      />
    </SettingsPage>
  );
}

/** What is never imported or grabbed: file patterns and blocked content, and failed releases. */
export function BlocklistsSettings() {
  const tier = useTier();
  return (
    <SettingsPage title="Blocklists">
      <UnwantedFiles />
      {/* Failed releases stay advanced (answered 2026-09-24): empty until acquisition runs. */}
      {isTierVisible(tier, "advanced") && <Blocklist />}
    </SettingsPage>
  );
}

export function TagsSettings() {
  return (
    <SettingsPage title="Tags">
      <Tags />
    </SettingsPage>
  );
}

export function NetworkSettings() {
  return (
    <SettingsPage title="Network" requiredTier="advanced">
      <Network />
    </SettingsPage>
  );
}

export function PlayerSettings() {
  return (
    <SettingsPage title="Player" requiredTier="normal">
      <Streaming />
    </SettingsPage>
  );
}

export function MetadataSettingsPage() {
  return (
    <SettingsPage title="Metadata" requiredTier="normal">
      <MetadataSettings />
    </SettingsPage>
  );
}

/** The caller's own interface: what the frame around every page shows. */
export function WebUISettings() {
  const [showCalendar, setShowCalendar] = useShowCalendar();
  const [showFooter, setShowFooter] = useShowFooter();
  return (
    <SettingsPage title="Web UI">
      <Card title="Layout" help="Your own view only; every other account keeps its own.">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={showCalendar} onChange={(event) => setShowCalendar(event.target.checked)} />
          Show Calendar in the sidebar
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={showFooter} onChange={(event) => setShowFooter(event.target.checked)} />
          Show the status bar along the bottom
        </label>
      </Card>
      {showFooter && <FooterItems />}
    </SettingsPage>
  );
}

const FOOTER_LABELS: Record<FooterItem, string> = {
  events: "What just happened (notifications)",
  work: "Work running now",
  queue: "Download queue",
  ip: "Public IP address",
  time: "Server time",
  version: "Version",
};

/** The status bar's items: each on or off, in the order they run left to right. */
function FooterItems() {
  const [items, setItems] = useFooterItems();
  const swap = (at: number, to: number) => {
    const next = [...items];
    [next[at], next[to]] = [next[to]!, next[at]!];
    setItems(next);
  };
  return (
    <Card title="Status bar" help="Which items the bar along the bottom shows, left to right. IP address and time start off, so screenshots give neither away. Showing the IP address asks an outside service which address this host connects from.">
      <ul className="flex max-w-md flex-col">
        {items.map((entry, at) => (
          <li key={entry.item} className="flex items-center gap-2 border-b border-border-subtle py-1 text-sm last:border-0">
            <label className="flex flex-1 items-center gap-2">
              <input
                type="checkbox"
                checked={entry.shown}
                onChange={(event) => setItems(items.map((other) => (other.item === entry.item ? { ...other, shown: event.target.checked } : other)))}
              />
              {FOOTER_LABELS[entry.item]}
            </label>
            <button type="button" aria-label={`Move ${FOOTER_LABELS[entry.item]} left`} disabled={at === 0} onClick={() => swap(at, at - 1)} className="rounded p-1 text-ink-muted hover:text-ink disabled:opacity-30">
              <ArrowUp aria-hidden className="size-3.5" />
            </button>
            <button type="button" aria-label={`Move ${FOOTER_LABELS[entry.item]} right`} disabled={at === items.length - 1} onClick={() => swap(at, at + 1)} className="rounded p-1 text-ink-muted hover:text-ink disabled:opacity-30">
              <ArrowDown aria-hidden className="size-3.5" />
            </button>
          </li>
        ))}
      </ul>
    </Card>
  );
}
