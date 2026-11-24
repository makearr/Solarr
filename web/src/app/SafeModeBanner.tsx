import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";

/** Says on a settings page that safe mode keeps what the page configures from running. */
export function SafeModeBanner() {
  const general = useQuery({ queryKey: ["general-settings"], queryFn: () => ok(api.GET("/settings/general")) });
  if (!general.data?.safe_mode) return null;
  return (
    <p role="status" className="rounded-md border border-warn p-2 text-sm">
      {t("safeMode.banner")}
    </p>
  );
}
