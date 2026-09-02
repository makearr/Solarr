import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

export interface ServerClock {
  /** Formatted in the server's zone, or null until the offset is known. */
  readonly time: string | null;
  readonly timezone: string | null;
  /** Milliseconds the viewer's clock is ahead of the server's. */
  readonly skewMs: number | null;
}

/** How far apart the two clocks may drift before it is worth saying so. */
export const SKEW_TOLERANCE_MS = 60_000;

/** The clock schedules, air dates and retention actually run on (section 19.2).
 *  Fetched once and then ticked locally against the measured offset, rather
 *  than polled: a request per second to render a clock is absurd, and the
 *  offset only changes if someone reconfigures the host. */
export function useServerClock(): ServerClock {
  const about = useQuery({
    queryKey: ["about"],
    queryFn: async () => {
      const { data, error } = await api.GET("/about");
      if (error) throw error;
      return data;
    },
    staleTime: Infinity,
  });

  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  const stamp = about.data?.server_time;
  const timezone = about.data?.timezone ?? null;
  if (!stamp || !timezone) return { time: null, timezone, skewMs: null };

  const served = Date.parse(stamp);
  if (Number.isNaN(served)) return { time: null, timezone, skewMs: null };

  // dataUpdatedAt is when this browser received the response, so the difference
  // is the offset plus one network hop -- close enough for a wall clock.
  const skewMs = about.dataUpdatedAt - served;

  return {
    time: new Intl.DateTimeFormat(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
      timeZone: timezone,
    }).format(new Date(now - skewMs)),
    timezone,
    skewMs,
  };
}
