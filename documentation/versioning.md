# Versioning

```text
MAJOR . MINOR . BUILD
  0   .   2   .  17
  │       │       └── successful Docker builds since this milestone
  │       └────────── milestone: something that works. Not a digit -- 0.9 is
  │                   followed by 0.10, then 0.11, for as long as it takes
  └────────────────── advanced only when the author says so. Never automatic
```

## BUILD — successful Docker builds

Advances **only** when `make image` completes successfully. Commits do not change it, so the
number means "this many images have been produced", not "this much has been typed".

A failed build leaves the version untouched.

### What `make image` does

```text
1  refuse if the working tree is dirty          ← see below
2  docker login ghcr.io
3  buildx --push, linux/amd64 + linux/arm64
4  bump BUILD in VERSION
5  prepend a CHANGELOG.md entry — commits since the previous tag
6  commit VERSION + CHANGELOG.md
7  annotated tag v<version>
8  git push main + tag
9  create the GitHub release, body = the changelog entry
```

Everything after step 3 runs only if the build and push succeeded, so a failed build leaves
the repository untouched.

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
| **GitHub release** | Published per build, body is the changelog entry plus the image reference and platforms |

Releases below `1.0.0` are marked as pre-releases.

## MINOR — milestones

Advanced by hand with `make milestone`, which increments MINOR and resets BUILD to 0. Each
milestone is **something that works**, not something that is written.

| Version | Milestone |
|---------|-----------|
| `0.0.x` | Pre-milestone — foundation, store, jobs, API skeleton |
| `0.1.x` | **Working web interface** |
| `0.2.x` | **Working music library, usenet only** |
| `0.3.x` | Working TV library |
| `0.4.x` | Working movie library |
| `0.5.x` | Working audiobook library |
| `0.6.x` | Working book library |
| `0.7.x` | Working adult library |
| `0.8.x` | Working comic library |
| `0.9.x` | Working ROM library |
| `0.10.x`, `0.11.x`, ... | Whatever comes after. **MINOR is a counter, not a digit** |

**MINOR does not stop at 9.** The nine library types happen to fit `0.1` to `0.9`, which is a
coincidence of counting and not a plan. Work continues past them -- `0.10.x`, `0.11.x` and
onwards -- and `make milestone` increments arithmetically, so `0.9.14` becomes `0.10.0`.

**`1.0.0` is not reached by finishing a list.** No milestone, no completed roadmap and no
amount of working library types advances MAJOR. It happens when the author decides it happens,
and nothing in the tooling can do it -- there is deliberately no `make major`.

**"Working" is a high bar.** A library type reaches its milestone when it can identify,
acquire, import and present that media end to end — not when its module compiles.

**`0.2.x` says "usenet only" because it has to.** Torrent support is stage 17 and music is
predominantly on torrents, so a milestone claiming a working music library without that
qualification would overstate what it does. The stage order is a deliberate risk and is not
changed by this; only the claim is. Stage 17 is M effort and can move at any point, and the
qualification comes off the milestone when it does.

## What the version is not

- Not a measure of effort or commits.
- Not automatically semver-meaningful below `1.0` — the API is unstable until then.
- **Not a progress bar.** `0.9` does not mean "90% done", and it is not one step from `1.0`.
- Not sortable as text. `0.10.0` is newer than `0.9.9` but sorts before it in any plain string
  comparison; use `sort -V` and git's own tag ordering, never `sort`.

## Injecting it into the binary

Build metadata is compiled in and surfaced at `/api/v1/about`, which is also how the AGPL §13
source obligation is met (see `licence-compliance.md`):

```bash
-ldflags "-X main.version=$(cat VERSION) -X main.commit=$(git rev-parse --short HEAD) \
          -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

A build with an unknown commit reports it as unknown rather than showing a plausible-looking
wrong value.
