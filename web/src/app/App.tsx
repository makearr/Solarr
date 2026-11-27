import { tr } from "@/lib/i18nRich";
import { useState } from "react";
import { ErrorBoundary } from "./ErrorBoundary";
import { RouterProvider } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { About } from "./About";
import { Login } from "./Login";
import { router } from "./router";
import { Setup } from "./Setup";
import { WIZARD_KEY, type WizardState } from "./setup/wizardState";

/** The setup gate, outside the router: an install with no administrator has
 *  nothing to route to, because every other endpoint answers 401. */
export function App() {
  // This tab only. Skipping is about the root folder, never about the account.
  const [skipped, setSkipped] = useState(false);

  const setup = useQuery({
    queryKey: ["setup"],
    queryFn: () => ok(api.GET("/setup")),
  });

  // Whether this caller has a session. Also how the interface discovers that the
  // server runs without authentication: there, /me simply answers.
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => ok(api.GET("/me")),
    retry: false,
  });

  // Where the setup assistant stopped, kept on the server: an unfinished one reopens in this tab and in any other browser.
  const preferences = useQuery({
    queryKey: ["me", "preferences"],
    enabled: me.isSuccess,
    queryFn: () => ok(api.GET("/me/preferences")),
  });
  const storedAssistant = (() => {
    try {
      const stored = preferences.data?.[WIZARD_KEY];
      const state = stored === undefined ? null : (JSON.parse(stored) as WizardState);
      return state !== null && state.v === 1 ? state : null;
    } catch {
      return null;
    }
  })();
  const unfinishedAssistant = storedAssistant !== null && !storedAssistant.finished && storedAssistant.step !== "start";
  // Finished or skipped stays closed after a reload; the dashboard's setup box then asks for the missing folder.
  const closedAssistant = storedAssistant?.finished === true;

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
        {tr("app.serverUnreachable", { path: <code className="font-mono">/api/v1</code> })}
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
  if (window.location.pathname === "/setup") return <Setup rerun onDone={() => window.location.assign("/")} />;
  if (!skipped && !closedAssistant && (!setup.data.has_root_folder || unfinishedAssistant)) {
    return <Setup onDone={() => setSkipped(true)} />;
  }

  return (
    <ErrorBoundary name="The application">
      <RouterProvider router={router} />
    </ErrorBoundary>
  );
}

function Splash({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <p className="max-w-sm text-center text-sm text-ink-muted">{children}</p>
    </div>
  );
}
