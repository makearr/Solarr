import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, ChevronRight, CircleUser } from "lucide-react";

import { api } from "@/api/client";
import { MODES, SCHEMES, type Mode } from "@/lib/theme";
import { useTheme } from "@/lib/useTheme";
import { useSetMyTier, type Tier } from "@/lib/useTier";

const TIERS: readonly { tier: Tier; label: string }[] = [
  { tier: "normal", label: "Normal" },
  { tier: "advanced", label: "Advanced" },
  { tier: "expert", label: "Expert" },
];

const TIER_RANK: Record<Tier, number> = { normal: 0, advanced: 1, expert: 2 };

const itemStyle = "flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas";

function Chosen() {
  return (
    <span className="size-3.5">
      <DropdownMenu.ItemIndicator>
        <Check aria-hidden className="size-3.5" />
      </DropdownMenu.ItemIndicator>
    </span>
  );
}

/** Light, dark or the OS's choice, and the colour scheme; the top bar keeps only the quick light/dark switch. */
function Appearance() {
  const theme = useTheme();
  const scheme = SCHEMES.find((entry) => entry.id === theme.scheme);
  return (
    <>
      <DropdownMenu.Label className="px-2 py-1 text-xs text-ink-muted">Appearance</DropdownMenu.Label>
      <DropdownMenu.RadioGroup value={theme.mode} onValueChange={(value) => theme.setMode(value as Mode)}>
        {MODES.map((entry) => (
          <DropdownMenu.RadioItem
            key={entry.id}
            value={entry.id}
            disabled={!theme.hasBothModes}
            onSelect={(event) => event.preventDefault()}
            className={`${itemStyle} data-[disabled]:opacity-50`}
          >
            <Chosen />
            {entry.label}
          </DropdownMenu.RadioItem>
        ))}
      </DropdownMenu.RadioGroup>
      <DropdownMenu.Sub>
        <DropdownMenu.SubTrigger className={`${itemStyle} data-[state=open]:bg-canvas`}>
          <span className="size-3.5" />
          <span className="flex-1">Colour scheme</span>
          <span className="text-xs text-ink-muted">{scheme?.label}</span>
          <ChevronRight aria-hidden className="size-3.5" />
        </DropdownMenu.SubTrigger>
        <DropdownMenu.Portal>
          <DropdownMenu.SubContent
            sideOffset={4}
            className="z-50 min-w-40 rounded-md border border-border-subtle bg-surface p-1 text-sm shadow-lg"
          >
            <DropdownMenu.RadioGroup value={theme.scheme} onValueChange={theme.setScheme}>
              {SCHEMES.map((entry) => (
                <DropdownMenu.RadioItem
                  key={entry.id}
                  value={entry.id}
                  onSelect={(event) => event.preventDefault()}
                  className={itemStyle}
                >
                  <Chosen />
                  {entry.label}
                </DropdownMenu.RadioItem>
              ))}
            </DropdownMenu.RadioGroup>
          </DropdownMenu.SubContent>
        </DropdownMenu.Portal>
      </DropdownMenu.Sub>
      <DropdownMenu.Separator className="my-1 h-px bg-border-subtle" />
    </>
  );
}

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
          <Appearance />
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
