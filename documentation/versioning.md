# Versioning

```text
MAJOR . MINOR . BUILD
  0   .   2   .  17
  │       │       └── successful Docker builds since this milestone
  │       └────────── milestone: each working library type
  └────────────────── 1.0 when every planned library type works
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
| `0.2.x` | **Working music library** |
| `0.3.x` | Working TV library |
| `0.4.x` | Working movie library |
| `0.5.x` | Working audiobook library |
| `0.6.x` | Working book library |
| `0.7.x` | Working adult library |
| `0.8.x` | Working comic library |
| `0.9.x` | Working ROM library |
| `1.0.0` | All planned library types working |

The nine library milestones fit `0.1` to `0.9` exactly, so `1.0` means the roadmap in
`private/architecture.md` Part IV is complete.

**"Working" is a high bar.** A library type reaches its milestone when it can identify,
acquire, import and present that media end to end — not when its module compiles. The stage
checklist in `private/todo.md` is the definition.

## What the version is not

- Not a measure of effort or commits.
- Not automatically semver-meaningful below `1.0` — the API is unstable until then.

## Injecting it into the binary

Build metadata is compiled in and surfaced at `/api/v1/about`, which is also how the AGPL §13
source obligation is met (see `licence-compliance.md`):

```bash
-ldflags "-X main.version=$(cat VERSION) -X main.commit=$(git rev-parse --short HEAD) \
          -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

A build with an unknown commit reports it as unknown rather than showing a plausible-looking
wrong value.
