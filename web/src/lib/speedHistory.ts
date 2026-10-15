/** One reading of the download clients' combined speed. */
export type SpeedSample = { at: number; bytesPerSec: number };

/** How many readings the dashboard's graph keeps: ten minutes at the queue's five-second refresh. */
export const SPEED_SAMPLES = 120;

/** appendSample adds a reading unless it repeats the last one's time, keeping the newest `max`. */
export function appendSample(samples: readonly SpeedSample[], sample: SpeedSample, max = SPEED_SAMPLES): SpeedSample[] {
  if (samples.at(-1)?.at === sample.at) return [...samples];
  return [...samples, sample].slice(-max);
}

/** totalSpeed adds up the clients that are running and could be read. */
export function totalSpeed(clients: readonly { paused: boolean; speed_bytes_per_sec: number; error?: string }[]): number {
  return clients.reduce((sum, client) => (client.paused || client.error ? sum : sum + client.speed_bytes_per_sec), 0);
}

/** sparkPath draws the readings as an SVG area path in a width × height box, the peak at the top. */
export function sparkPath(samples: readonly SpeedSample[], width: number, height: number): string {
  if (samples.length < 2) return "";
  const peak = Math.max(1, ...samples.map((sample) => sample.bytesPerSec));
  const step = width / (samples.length - 1);
  const points = samples.map((sample, i) => `${(i * step).toFixed(1)},${(height - (sample.bytesPerSec / peak) * height).toFixed(1)}`);
  return `M0,${height} L${points.join(" L")} L${width},${height} Z`;
}
