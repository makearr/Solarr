export type RemoteImageSize = "thumb" | "small" | "full";

/** A provider's picture as Solarr serves it from its own cache, so the browser never asks the provider's CDN. */
export function remoteImage(address: string, size: RemoteImageSize = "small"): string {
  return `/api/v1/images/remote?url=${encodeURIComponent(address)}&size=${size}`;
}
