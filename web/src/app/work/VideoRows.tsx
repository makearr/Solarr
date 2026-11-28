import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";

import { CoverArt } from "../CoverArt";
import { WorkStart } from "./WorkStart";

export interface VideoRef {
  id: number;
  title: string;
  year?: number;
}

/** One video work: its preview picture, title, and the shared Play button once the work's files are known. */
function VideoRow({ video, categoryId }: { video: VideoRef; categoryId: string | undefined }) {
  const detail = useQuery({ queryKey: ["work", video.id], queryFn: () => ok(api.GET("/works/{id}", { params: { path: { id: video.id } } })) });
  return (
    <li className="flex items-center gap-3 px-3 py-2 text-sm">
      <CoverArt workId={video.id} title={video.title} size="thumb" className="h-10 w-16 shrink-0 rounded object-cover" />
      <Link
        {...(categoryId
          ? ({ to: "/library/$categoryId/works/$workId", params: { categoryId, workId: String(video.id) } } as const)
          : ({ to: "/works/$id", params: { id: String(video.id) } } as const))}
        className="min-w-0 flex-1 truncate hover:underline">
        {video.title}
      </Link>
      {video.year != null && <span className="shrink-0 text-xs text-ink-muted">{video.year}</span>}
      <span className="w-24 shrink-0">{detail.data && <WorkStart work={detail.data} categoryId={categoryId} />}</span>
    </li>
  );
}

/** A list of video works under a heading, each playable from the row; nothing when there are none. */
export function VideoRows({ videos, categoryId }: { videos: readonly VideoRef[]; categoryId: string | undefined }) {
  if (videos.length === 0) return null;
  return (
    <section className="flex flex-col gap-1.5">
      <h3 className="text-xs font-medium uppercase tracking-wide text-ink-muted">
        {t("videos.heading")} ({videos.length})
      </h3>
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {videos.map((video) => (
          <VideoRow key={video.id} video={video} categoryId={categoryId} />
        ))}
      </ul>
    </section>
  );
}

const ALBUM_ID_PROVIDER = "musicbrainz:release-group";
const VIDEO_ALBUM_PROVIDERS = ["musicbrainz-live-video", "musicbrainz-video-album"];

/** The videos of an album: the concert filmed as it and the music videos of its tracks, tied by the album's release group. */
export function AlbumVideos({ externalIds, categoryId }: { externalIds: readonly { provider: string; external_id: string }[]; categoryId: string | undefined }) {
  const releaseGroup = externalIds.find((id) => id.provider === ALBUM_ID_PROVIDER)?.external_id;
  const linked = releaseGroup ? VIDEO_ALBUM_PROVIDERS.map((provider) => `${provider}=${releaseGroup}`).join(",") : "";
  const videos = useQuery({
    queryKey: ["works", "linked", linked],
    enabled: linked !== "",
    queryFn: () => ok(api.GET("/works", { params: { query: { linked, limit: 50 } } })),
  });
  const items = (videos.data?.items ?? []).map((work) => ({ id: work.id, title: work.title, year: work.year }));
  return <VideoRows videos={items} categoryId={categoryId} />;
}
