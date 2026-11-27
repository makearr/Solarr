import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, Circle, X } from "lucide-react";

import { api, ok } from "@/api/client";
import { t, type MessageKey } from "@/lib/i18n";
import { useSetupDismissed } from "@/lib/interfacePreferences";
import { useLibraries, useRootFolders } from "@/lib/resources";

interface Step {
  readonly key: string;
  readonly label: MessageKey;
  readonly to: string;
  readonly done: boolean;
}

/** What an administrator still has to connect before Solarr does its job; gone once all is done, or when dismissed. */
export function SetupChecklist() {
  const [dismissed, setDismissed] = useSetupDismissed();
  const libraries = useLibraries();
  const rootFolders = useRootFolders();
  const indexers = useQuery({ queryKey: ["indexers"], queryFn: () => ok(api.GET("/indexers")) });
  const downloaders = useQuery({ queryKey: ["downloaders"], queryFn: () => ok(api.GET("/downloaders")) });
  const providers = useQuery({
    queryKey: ["metadata-providers"],
    queryFn: async () => {
      const { data, error } = await api.GET("/metadata/providers");
      if (error) throw error;
      return data.providers;
    },
  });
  if (dismissed || !libraries.data || !rootFolders.data || !indexers.data || !downloaders.data || !providers.data) return null;

  const steps: Step[] = [
    { key: "library", label: "setup.library", to: "/settings/libraries", done: libraries.data.length > 0 && rootFolders.data.length > 0 },
    { key: "indexer", label: "setup.indexer", to: "/connections/indexers", done: indexers.data.length > 0 },
    { key: "downloader", label: "setup.downloader", to: "/connections/downloaders", done: downloaders.data.length > 0 },
    { key: "metadata", label: "setup.metadata", to: "/settings/metadata", done: !providers.data.some((provider) => provider.enabled && provider.needs_key) },
  ];
  if (steps.every((step) => step.done)) return null;
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between">
        <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{t("setup.title")}</h2>
        <button
          type="button"
          onClick={() => setDismissed(true)}
          aria-label={t("setup.dismiss")}
          title={t("setup.dismiss")}
          className="rounded-md p-0.5 text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-accent"
        >
          <X aria-hidden className="size-4" />
        </button>
      </div>
      <ol className="flex flex-col gap-1">
        {steps.map((step) => (
          <li key={step.key}>
            <Link
              to={step.to}
              className="flex items-center gap-2 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm hover:border-accent
                         focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              {step.done
                ? <Check aria-hidden className="size-4 shrink-0 text-ok" />
                : <Circle aria-hidden className="size-4 shrink-0 text-ink-muted" />}
              <span className={step.done ? "text-ink-muted line-through" : ""}>{t(step.label)}</span>
            </Link>
          </li>
        ))}
      </ol>
    </section>
  );
}
