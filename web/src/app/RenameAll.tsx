import { useState } from "react";

import { useLibraryLabel } from "@/lib/libraryLabel";
import { useCategories } from "@/lib/resources";
import { Button } from "@/ui/Button";

import { RenamePlanner } from "./RenamePlanner";

/** Every library's files named as its naming says: a preview of what would change, library by library, then the rename. */
export function RenameAll() {
  const categories = useCategories();
  const label = useLibraryLabel();
  const [previewing, setPreviewing] = useState(false);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-4xl flex-col gap-4 p-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-base font-semibold">Rename files</h1>
          <Button className="ml-auto" disabled={previewing} onClick={() => setPreviewing(true)}>
            {previewing ? "Previewing every library" : "Preview every library"}
          </Button>
        </div>
        <p className="max-w-2xl text-xs text-ink-muted">
          Brings the files of your libraries into line with each library&apos;s naming (Library → Settings → Naming). Nothing changes until
          you press Rename under a library&apos;s preview; files are moved by the same engine as every move, and can be found again under
          Storage → Folders.
        </p>
        {categories.data?.map((category) => (
          <section key={`${category.id}-${previewing}`} className="rounded-lg border border-border-subtle bg-surface p-4">
            <RenamePlanner title={<h2 className="text-sm font-medium">{label(category)}</h2>} categoryId={category.id} previewAtOnce={previewing} />
          </section>
        ))}
        {categories.data?.length === 0 && <p className="text-sm text-ink-muted">No library yet.</p>}
      </div>
    </div>
  );
}
