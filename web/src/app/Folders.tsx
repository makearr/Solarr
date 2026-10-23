import { DownloadFolderSection } from "./DownloadFolders";
import { MoveJobList } from "./Moves";
import { RootFolderSection } from "./RootFolders";

/** Storage -> Folders: every folder Solarr reads or writes, library roots and download folders on one page (user,
 *  2026-10-23), then the moves running between them. */
export function Folders() {
  return (
    <div className="mx-auto flex h-full max-w-4xl flex-col gap-6 overflow-y-auto p-6">
      <h1 className="text-lg font-semibold">Folders</h1>
      <RootFolderSection />
      <DownloadFolderSection />
      <MoveJobList />
    </div>
  );
}
