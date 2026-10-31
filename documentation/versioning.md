# Versioning

```text
YEAR . MONTH . RELEASE . BUILD
2026 .  10   .    1    .  0512
 │       │        │        └── build    +1 on every successful build or GitHub push; never resets
 │       │        └─────────── release  +1 on every release; 1 on the first release of a month
 │       └──────────────────── month    of the latest release, two digits
 └──────────────────────────── year     of the latest release, four digits
```

`YYYY.MM.R.BBBB`, in use since 2026-10-31 (`YYYY.MM.RRR.BBB` from 2026-10-25 to then). `release` is not padded;
`build` is zero-padded to four digits and simply grows a fifth past 9999. A **release** is a build published on the maintainer's
explicit word (`make image`); everything else — a local image, a push — advances only `build`, so
any commit or image can be traced back to one number. Until the first release of a month, builds
keep the year, month and release of the previous one (`2026.10.0.0510` means "no release yet
under this scheme").

**There is no MAJOR or MINOR, and no `1.0`.** Milestones survive as a **name**, not a version
number (see below); nothing in this scheme is ever "reached".

The four parts are stored and compared separately, never as one parsed decimal — `.09` read as
`9` loses the padding and breaks the format.

## What advances what

| Action | Advances | Writes |
|---|---|---|
| `make image-local` | `build` | `VERSION` only; no push, tag or release. Image stamped `-dirty` when the tree is |
| `make push` | `build` | commits `VERSION` as `Build <version>` and pushes `main`; a failed push undoes the commit |
| `make image` | `release` and `build`, year and month set to today | everything below |

A failed build or push leaves the version untouched.

### What `make image` does

```text
1  refuse if the working tree is dirty                       ← see below
2  compute the release version from today's date + VERSION
3  build each platform's filesystem and scan it for private keys (scripts/scan-image-secrets.sh)
4  docker login ghcr.io and Docker Hub (credentials from private/secrets.md, never in the image)
5  buildx --push, linux/amd64 + linux/arm64, to ghcr.io/makearr/solarr and makearr/solarr,
   tagged with the version and latest
6  prepend a CHANGELOG.md entry — commits since the previous tag
7  commit VERSION + CHANGELOG.md
8  annotated tag v<version>
9  git push main + tag
10 create the GitHub release, body = the changelog entry, title includes the milestone name if set
```

Nothing is pushed if the scan finds a value from `private/secrets.md` in either image. The one
key compiled in on purpose (TheTVDB project key) is marked `shippable` there and skipped.
`make scan-image REF=<image>` runs the same scan on any local image.

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
| `CHANGELOG.md` | One section per release, newest first, in the repository |
| Annotated git tag | The same entry, attached to `v<version>` |
| **GitHub release** | Published per release, body is the changelog entry plus the image references, platforms, and the milestone name if one is set |

## Milestones — names, not numbers

`make milestone NAME="music, usenet"` records a name in the `MILESTONE` file at the repository root,
sitting beside `VERSION`. It does not change any version component. The name shows up:

- In the GitHub release title: `2026.10.1.0512 — music, usenet`
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
- Not sortable as plain text once `release` reaches 10; use `sort -V` and git's own tag ordering.

## Injecting it into the binary

Build metadata is compiled in and surfaced at `/api/v1/about`, which is also how the AGPL §13
source obligation is met (see `licence-compliance.md`):

```bash
-ldflags "-X main.version=$(cat VERSION) -X main.commit=$(git rev-parse --short HEAD) \
          -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X main.milestone=$(cat MILESTONE)"
```

A build with an unknown commit reports it as unknown rather than showing a plausible-looking
wrong value. `milestone` is simply empty when `MILESTONE` is empty or absent.
