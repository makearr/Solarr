import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Check } from "lucide-react";

/** The tick of a chosen menu item, its space kept when unchosen so the labels line up. */
export function MenuCheck() {
  return (
    <span className="size-3.5">
      <DropdownMenu.ItemIndicator>
        <Check aria-hidden className="size-3.5" />
      </DropdownMenu.ItemIndicator>
    </span>
  );
}
