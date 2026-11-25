# Contributing to Solarr

Thank you for considering it. The short version of how to help is in the [README](README.md#how-you-can-help); this
page is the long version.

## Reports: the most valuable help there is

Solarr has been tested on one person's library, and yours is different.

- **Bugs.** Open an [issue](https://github.com/makearr/Solarr/issues) with: the Solarr version (bottom right of the
  web interface), what you did, what you expected, what happened, and the relevant lines from *Settings → Logs* (the
  messages, not the whole log; no keys or addresses). Screenshots of confusing screens are gold.
- **Recognition problems.** A file matched to the wrong album, edition, series, episode, film or book, or not matched at
  all: give the file name and its folder, the embedded tags (`ffprobe -hide_banner -show_format <file>`), what Solarr
  chose and the correct item at a provider (a MusicBrainz release, a TheTVDB/TMDB page). Anonymise titles if you prefer;
  keep the *shape* of the name, which is usually what matters.
- **\*Arr import problems.** Which \*Arr and version, roughly how much it holds, what the preview showed, what came
  across wrong. Solarr only reads from your \*Arr, so testing it is safe.
- **Benchmarks.** How long the first scan of your library took, on what hardware and storage.
- **Hardware and software the project does not have:** NVIDIA GPUs (transcoding), Plex, large libraries, unusual
  filesystems.

The [AI agent briefs](agents/README.md) turn any coding agent into a tester that produces these reports for you.

## Security

Vulnerabilities go privately through [GitHub's advisory form](https://github.com/makearr/Solarr/security/advisories/new),
never as a public issue. See [SECURITY.md](SECURITY.md).

## Code

Solarr stays **privacy-first, open access and user-friendly**. A contribution that adds telemetry, an account
requirement, a paywall or a screen only an expert can read will not be merged, however good the code.

Code contributions are welcome, with two conditions:

- **Ask first for anything bigger than a bug fix.** Open an issue so your time is not spent on something that collides
  with where the project is going. That goes double for AI agents: if you want to point yours at Solarr for more than a
  report, get in touch first, so your agents and the project's do not end up fighting over who owns the import code.
- **Keep up.** Solarr moves fast. A pull request targets the `dev` branch and must pass `make verify` (formatting, vet,
  strict linting, tests, generated code up to date, licences). New code comes with tests, comments say *why* in one line,
  and the architecture's rules hold (one generic core, nothing deleted directly, everything from the web interface).

Start with the [codebase map](documentation/codebase-map.md), the [coding standards](documentation/coding-standards.md),
the [architecture](documentation/architecture.md) and [building it yourself](documentation/building.md).

## Licence

By contributing you agree that your contribution is licensed under [AGPL-3.0-or-later](LICENSE).
