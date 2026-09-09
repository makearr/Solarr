import { useParams } from "@tanstack/react-router";

const SECTIONS: Record<string, { title: string; description: string }> = {
  quality: {
    title: "Quality profiles",
    description:
      "internal/score already interprets tier-list-or-target rules (ADR-40) and " +
      "internal/settings.Cache already has full CRUD for them, but there is no REST API " +
      "or builder UI to create one by hand yet -- no named default profile ships either.",
  },
  naming: {
    title: "Naming",
    description:
      "internal/naming already compiles and renders templates, and the shipped music " +
      "default reproduces the existing library byte for byte (section 11.8), but there is " +
      "no template editor or a naming_profile table for a second one to live in yet.",
  },
  language: {
    title: "Language",
    description:
      "internal/settings.Cache already has full CRUD for language profiles (required/" +
      "preferred audio and subtitle languages, ADR-40's language axis), but there is no " +
      "REST API or builder UI to create one by hand yet.",
  },
};

/** A placeholder for the three per-library config sections ADR-54's own
 *  tree sketch lists beside a library's entity levels (Quality profiles,
 *  Naming, Language). Confirmed with the user 2026-09-09: none of the three
 *  has a real screen today (only Settings.Cache-level CRUD, for two of
 *  them) -- this lands the navigation slot honestly, real builder UIs are
 *  their own immediate follow-up rather than pretended finished here. */
export function LibraryConfigPlaceholder() {
  const { section } = useParams({ strict: false }) as { section?: string };
  const info = (section && SECTIONS[section]) || null;

  if (!info) {
    return <p className="p-6 text-sm text-ink-muted">Unknown configuration section.</p>;
  }

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-3 p-6">
      <h1 className="text-lg font-semibold">{info.title}</h1>
      <p className="text-sm leading-relaxed text-ink-muted">{info.description}</p>
      <p className="text-sm text-ink-muted">Not built yet.</p>
    </div>
  );
}
