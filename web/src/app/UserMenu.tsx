import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, CircleUser } from "lucide-react";

import { api } from "@/api/client";
import { useSetMyTier, type Tier } from "@/lib/useTier";

const TIERS: readonly { tier: Tier; label: string }[] = [
  { tier: "normal", label: "Normal" },
  { tier: "advanced", label: "Advanced" },
  { tier: "expert", label: "Expert" },
];

const TIER_RANK: Record<Tier, number> = { normal: 0, advanced: 1, expert: 2 };

/** Top right, as in the *Arrs: who is signed in, how much of the interface they see, and the way out. */
export function UserMenu() {
  const queryClient = useQueryClient();
  const setTier = useSetMyTier();
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const { data, error } = await api.GET("/me");
      if (error) throw error;
      return data;
    },
  });
  if (!me.data) return null;
  const { tier, tier_max: tierMax, username, auth_required: authRequired } = me.data;

  // Clearing the session makes /me answer 401, which is what brings the sign-in screen back.
  const signOut = async () => {
    await api.POST("/auth/logout");
    await queryClient.resetQueries();
  };

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-ink-muted hover:text-ink
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <CircleUser aria-hidden className="size-4" />
        <span>{username ?? "Account"}</span>
        <ChevronDown aria-hidden className="size-3.5" />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={4}
          className="z-50 min-w-48 rounded-md border border-border-subtle bg-surface p-1 text-sm shadow-lg"
        >
          <DropdownMenu.Label className="px-2 py-1 text-xs text-ink-muted">Interface level</DropdownMenu.Label>
          <DropdownMenu.RadioGroup value={tier} onValueChange={(value) => setTier.mutate(value as Tier)}>
            {TIERS.filter((entry) => TIER_RANK[entry.tier] <= TIER_RANK[tierMax]).map((entry) => (
              <DropdownMenu.RadioItem
                key={entry.tier}
                value={entry.tier}
                className="flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none
                           data-[highlighted]:bg-canvas"
              >
                <span className="size-3.5">
                  <DropdownMenu.ItemIndicator>
                    <Check aria-hidden className="size-3.5" />
                  </DropdownMenu.ItemIndicator>
                </span>
                {entry.label}
              </DropdownMenu.RadioItem>
            ))}
          </DropdownMenu.RadioGroup>
          {authRequired && (
            <>
              <DropdownMenu.Separator className="my-1 h-px bg-border-subtle" />
              <DropdownMenu.Item
                onSelect={() => void signOut()}
                className="cursor-default rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
              >
                Switch user
              </DropdownMenu.Item>
              <DropdownMenu.Item
                onSelect={() => void signOut()}
                className="cursor-default rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
              >
                Log out
              </DropdownMenu.Item>
            </>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
