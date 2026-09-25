# Versioning

```text
YEAR . MONTH . RELEASE . BUILD
 26  .  09   .    1    .  35
 │       │        │        └── build   never resets, ever. Count of successful images, all time
 │       │        └─────────── release resets to 1 on the first release of each month
 │       └──────────────────── month   zero-padded, always two digits
 └──────────────────────────── year    two digits
```

Both `release` and `build` advance on every successful `make image`. `release` is simply the
monthly-scoped count: the first successful build in a calendar month is release 1, the next is
release 2, and so on until the month rolls over. `build` is the same all-time counter it always
was, just relabelled — it never resets.

**There is no MAJOR or MINOR, and no `1.0`.** Milestones survive as a **name**, not a version
number (see below); nothing in this scheme is ever "reached".

**The month is always two digits.** `year`, `month`, `release` and `build` are stored and
compared as four separate parts, never as one parsed decimal — treating `.09` as `9` loses the
padding and breaks the string format.

## BUILD and RELEASE — advanced by every successful image

Both advance when `make image` **or `make image-local`** completes successfully (the latter since
2026-09-25). Commits do not change them, so the numbers mean "this many images have been
produced" and "this many this month", not "this much has been typed". `make image-local` writes
the new `VERSION` and nothing else: no push, tag or release. Its image carries `-dirty` when the
tree had uncommitted changes.

A failed build leaves the version untouched.

### What `make image` does

```text
1  refuse if the working tree is dirty                    ← see below
2  compute the next version from today's date + VERSION   ← same month: release+1, new month: release=1; build+1 always
3  docker login ghcr.io
4  buildx --push, linux/amd64 + linux/arm64, tagged with the next version, YEAR.MONTH, and latest
5  prepend a CHANGELOG.md entry — commits since the previous tag
6  commit VERSION + CHANGELOG.md
7  annotated tag v<version>
8  git push main + tag
9  create the GitHub release, body = the changelog entry, title includes the milestone name if set
```

Everything after step 4 runs only if the build and push succeeded, so a failed build leaves the
repository untouched.

### Why a dirty tree is refused

The About page reports the commit the binary was built from, which is how the AGPL §13 source
obligation is met (`licence-compliance.md`). Building from uncommitted changes would stamp a
commit that does not describe the running binary — the source offered would not correspond to
the software. That is precisely the misrepresentation §13 exists to prevent, so it is a hard
failure rather than a warning.

**`make image-local`** exists for iteration: single-arch, loaded locally, tolerates a dirty
tree, pushes nothing, and stamps the version `-dirty` so such an image is never mistaken for a
release.

### Where changes are documented

| Surface | Content |
|---|---|
| `CHANGELOG.md` | One section per build, newest first, in the repository |
| Annotated git tag | The same entry, attached to `v<version>` |
| **GitHub release** | Published per build, body is the changelog entry plus the image reference, platforms, and the milestone name if one is set |

## Milestones — names, not numbers

`make milestone NAME="music, usenet"` records a name in the `MILESTONE` file at the repository root,
sitting beside `VERSION`. It does not change any version component. The name shows up:

- In the GitHub release title: `26.09.3 — music, usenet`
- On the About page and `/api/v1/about`, next to the version, when one is set
- In `solarr version`'s CLI output

A milestone is a claim that **something works**, named by the person deciding it works — not a
counter, and nothing advances it automatically. Naming a new milestone (or renaming the current
one) simply overwrites `MILESTONE`; there is no history of past names beyond what git already
keeps for the file.

## What the version is not

- Not a measure of effort or commits.
- Not semver. There is no API-stability contract implied by any component.
- **Not a progress bar.** A higher `release` does not mean "more done this month" — some months
  ship once, some ship ten times, depending on what landed.
- Sortable as text **only because every component is fixed-width or left-padded to compare
  correctly within its own field** (`26.09` before `26.10`, etc.) — still prefer `sort -V` and
  git's own tag ordering over a plain string comparison, since `release` and `build` are not
  zero-padded.

## Injecting it into the binary

Build metadata is compiled in and surfaced at `/api/v1/about`, which is also how the AGPL §13
source obligation is met (see `licence-compliance.md`):

```bash
-ldflags "-X main.version=$(cat VERSION) -X main.commit=$(git rev-parse --short HEAD) \
          -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X main.milestone=$(cat MILESTONE)"
```

A build with an unknown commit reports it as unknown rather than showing a plausible-looking
wrong value. `milestone` is simply empty when `MILESTONE` is empty or absent.
