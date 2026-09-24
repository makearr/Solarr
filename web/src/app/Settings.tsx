import { Blocklist } from "./Blocklist";
import { Categories } from "./Categories";
import { MetadataSettings } from "./MetadataSettings";
import { Network } from "./Network";
import { Priority } from "./Priority";
import { UnwantedFiles } from "./UnwantedFiles";
import { isTierVisible, useTier } from "@/lib/useTier";

/** Settings -> General, split apart on the 2026-09-19 review: Libraries, Blocklists, Network,
 *  Metadata. Quality profiles, indexers and download clients live where they are configured. */
function SettingsPage({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <h1 className="text-base font-semibold">{title}</h1>
        {children}
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

export function NetworkSettings() {
  return (
    <SettingsPage title="Network">
      <Network />
    </SettingsPage>
  );
}

export function MetadataSettingsPage() {
  return (
    <SettingsPage title="Metadata">
      <MetadataSettings />
    </SettingsPage>
  );
}
