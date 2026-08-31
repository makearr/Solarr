# Attribution

MediaNexus is licensed **AGPL-3.0-or-later** (see `LICENSE`).

This file records third-party code incorporated into MediaNexus and the obligations that come
with it. **Update it in the same commit as the code it describes** — reconstructing provenance
later is unreliable, and AGPL-3.0 §5 requires that modified files carry prominent notice.

---

## Incorporated code

*None yet.*

When code is incorporated, record it as:

```
### <component> — from <project> (<licence>)

- Source: <repository>, <commit or version>
- Taken:  <what exactly — files, functions, data tables>
- Changed: <how it was modified>
- Files:  <where it lives in this repository>
```

---

## Sanctioned reuse

Radarr, Sonarr, Lidarr, Readarr, Whisparr and Prowlarr are **GPL-3.0**, which is compatible
with AGPL-3.0. Reuse is deliberately limited to two areas:

| Area | Rationale |
|---|---|
| **Release-name parsing** — regexes, token tables, season/episode format handling | A decade of accreted edge cases that is hard to rebuild and boring to get wrong |
| **Quality definitions** — resolution, source, codec and edition token vocabularies | Shared vocabulary with the wider ecosystem; divergence would be a bug, not a feature |

**Anything beyond these two areas requires an explicit decision** with the trade-offs stated
before it is taken. The reason is not legal but architectural: the more that is imported, the
more the design drifts back toward the one MediaNexus exists to move away from. When something
outside this scope looks worth taking, raise it — do not simply take it.

## Not sanctioned

- **Servarr's hosted metadata services** (`skyhook.sonarr.tv`, `api.radarr.video`,
  `api.lidarr.audio`). This is not a licensing question: they are someone else's
  infrastructure and bandwidth, provided for their own applications. MediaNexus uses its own
  TMDB, TVDB and MusicBrainz credentials.
- Trademarks and branding. The names and logos are not covered by the source licence.

---

## Obligations this licence creates

1. **Source availability on network use** (AGPL §13). Anyone running a modified MediaNexus as
   a network service must offer users its complete source. The web UI carries a visible link
   to the source for the running version — see `documentation/licence-compliance.md`.
2. **Modified files carry notice** of the change and its date.
3. **Copyright notices are preserved** in anything derived from another project.
4. **Derivative works stay AGPL-3.0-or-later.**
