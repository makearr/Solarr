import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";

/** What one automatic search came to, in a few words. */
export function grabNote(result: components["schemas"]["GrabResult"]): string {
  if (result.already_downloading) return t("grab.alreadyDownloading");
  if (result.held) return t("grab.held");
  if (result.sent) return result.refused ? `${t("grab.sent")} (it refused ${result.refused} better first)` : t("grab.sent");
  if (result.refused) return "The download client refused every release found";
  return result.found === 0 ? t("grab.nothingFound") : t("grab.noneSendable");
}
