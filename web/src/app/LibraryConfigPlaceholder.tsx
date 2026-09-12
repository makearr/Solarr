import { useParams } from "@tanstack/react-router";

const SECTIONS: Record<string, { title: string; description: string }> = {
  naming: {
    title: "Naming",
    description:
      "internal/moving/naming already compiles and renders templates, and the shipped music " +
      "default reproduces the existing library byte for byte (section 11.8), but there is " +
      "no template editor or a naming_profile table for a second one to live in yet.",
  },
};

/** A placeholder for the per-library config sections ADR-54's own tree
 *  sketch lists beside a library's entity levels that still have no real
 *  screen. Quality and language profiles got theirs 2026-09-12
 *  (QualityProfiles.tsx, LanguageProfiles.tsx, their own routes registered
 *  ahead of this catch-all) -- only Naming is left standing in here. */
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
