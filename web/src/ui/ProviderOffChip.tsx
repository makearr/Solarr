import { Link } from "@tanstack/react-router";
import { t } from "@/lib/i18n";
import { Chip } from "./Chip";

/** The one way every screen says a provider is switched off globally: a chip that leads to where its switch is (Settings, Metadata). */
export function ProviderOffChip() {
  return (
    <Chip tone="warn">
      <Link to="/settings/metadata" className="underline">
        {t("provider.switchedOff")}
      </Link>
    </Chip>
  );
}
