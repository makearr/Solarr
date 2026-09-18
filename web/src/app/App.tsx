import { useState } from "react";
import { RouterProvider } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import { About } from "./About";
import { Login } from "./Login";
import { router } from "./router";
import { Setup } from "./Setup";

/** The setup gate, outside the router: an install with no administrator has
 *  nothing to route to, because every other endpoint answers 401. */
export function App() {
  // This tab only. Skipping is about the root folder, never about the account.
  const [skipped, setSkipped] = useState(false);

  const setup = useQuery({
    queryKey: ["setup"],
    queryFn: async () => {
      const { data, error } = await api.GET("/setup");
      if (error) throw error;
      return data;
    },
  });

  // Whether this caller has a session. Also how the interface discovers that the
  // server runs without authentication: there, /me simply answers.
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const { data, error } = await api.GET("/me");
      if (error) throw error;
      return data;
    },
    retry: false,
  });

  // Reachable before setup, before login, or fully logged in -- AGPL section
  // 13's "all users interacting with it remotely" is not limited to those who
  // can authenticate (documentation/licence-compliance.md). Checked after
  // every hook above, or React renders fewer hooks on some passes than others.
  if (window.location.pathname === "/about") return <About />;

  if (setup.isLoading) return <Splash>Starting…</Splash>;

  // Said out loud rather than fallen through. Treating an unreadable setup
  // state as "configured" is what hid a broken API base URL: the wizard never
  // ran and every list rendered empty, with nothing on screen to say why.
  if (setup.error !== null || !setup.data) {
    return (
      <Splash>
        The server could not be reached. Check that Solarr is running and that any reverse
        proxy forwards <code className="font-mono">/api/v1</code>.
      </Splash>
    );
  }

  // Not skippable: with no account there is nobody to authenticate as, so the
  // rest of the interface cannot load at all.
  if (setup.data.needs_administrator) return <Setup onDone={() => setSkipped(true)} />;

  if (me.isLoading) return <Splash>Starting…</Splash>;
  // An account exists but this caller has no session.
  if (me.error !== null) return <Login />;

  // Skippable: without a root folder the library stays empty, but everything
  // else works and the dashboard says what is missing.
  if (!skipped && !setup.data.has_root_folder) {
    return <Setup onDone={() => setSkipped(true)} />;
  }

  return <RouterProvider router={router} />;
}

function Splash({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <p className="max-w-sm text-center text-sm text-ink-muted">{children}</p>
    </div>
  );
}
