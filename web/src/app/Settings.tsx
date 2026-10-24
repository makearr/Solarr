import { ArrowDown, ArrowUp } from "lucide-react";

import { Blocklist } from "./Blocklist";
import { Tags } from "./Tags";
import { Categories } from "./Categories";
import { MetadataSettings } from "./MetadataSettings";
import { Network } from "./Network";
import { Priority } from "./Priority";
import { UnwantedFiles } from "./UnwantedFiles";
import { useFooterItems, useShowCalendar, useShowFooter, type FooterItem } from "@/lib/interfacePreferences";
import { isTierVisible, useTier, type Tier } from "@/lib/useTier";

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

/** Every library, and how new content is prioritised across them. */
export function LibrariesSettings() {
  return (
    <SettingsPage title="Libraries">
      <Categories />
      <Priority />
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
    <Card title="Status bar" help="Which items the bar along the bottom shows, left to right. IP address and time start off, so screenshots give neither away.">
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
