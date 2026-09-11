import { Link } from "@tanstack/react-router";

import { Categories } from "./Categories";
import { Network } from "./Network";
import { Priority } from "./Priority";
import { UnwantedFiles } from "./UnwantedFiles";

/** Settings sections in the order they matter to someone setting the thing up.
 *  `stage` names what has to exist before a section can do anything, so the
 *  scaffolding is honest about being scaffolding rather than looking broken.
 *  Storage moved out to its own sidebar section (todo 4.18) -- configuration
 *  lives where the thing it configures lives, and root folders are no
 *  longer just-a-list here. */
const SECTIONS = [
  { id: "profiles", title: "Quality profiles", blurb: "Hard gates, tier order and scoring.", stage: 4 },
  { id: "indexers", title: "Indexers", blurb: "Newznab and Torznab sources, and their egress.", stage: 5 },
  { id: "downloaders", title: "Download clients", blurb: "Usenet, torrent and direct downloads.", stage: 5 },
  { id: "metadata", title: "Metadata", blurb: "Providers, field priority and refresh cadence.", stage: 3 },
] as const;

export function GeneralSettings() {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">General</h1>
          <p className="text-xs text-ink-muted">
            Every setting is normal, advanced or expert (ADR-26). The tier selector arrives with
            the forms themselves.
          </p>
        </header>

        <Categories />

        <UnwantedFiles />

        <Network />

        <Priority />

        <section className="flex flex-col gap-2">
          <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Import</h2>
          <Link
            to="/settings/arr"
            className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface
                       px-3 py-2.5 hover:border-accent focus-visible:outline-2
                       focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            <span className="text-sm font-medium">*Arr import</span>
            <span className="text-xs text-ink-muted">
              Bring in what Lidarr (and, once their own stage ships, Sonarr/Radarr/Readarr/
              Whisparr) already monitors, as intent -- todo 4.13.
            </span>
          </Link>
        </section>

        <section className="flex flex-col gap-2">
          <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Sections</h2>
          <ul className="grid gap-3 sm:grid-cols-2">
            {SECTIONS.map((section) => (
              <li
                key={section.id}
                className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface
                           px-3 py-2.5"
              >
                <span className="flex items-baseline gap-2">
                  <span className="text-sm font-medium">{section.title}</span>
                  <span className="ml-auto shrink-0 rounded-full border border-border-subtle
                                   px-1.5 py-px text-[0.6875rem] text-ink-muted">
                    stage {section.stage}
                  </span>
                </span>
                <span className="text-xs text-ink-muted">{section.blurb}</span>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </div>
  );
}
