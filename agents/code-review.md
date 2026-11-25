# Brief: review a section of Solarr's code

You are an AI agent reviewing part of **Solarr** (https://github.com/makearr/Solarr), a self-hosted media manager
written in Go (backend) and TypeScript/React (web interface). Your job: pick **one area**, read it carefully, and report
**bugs, performance traps and security vulnerabilities**, with evidence. Quality over quantity: three real findings beat
thirty guesses.

## Rules

1. **Read only.** Do not push, open pull requests or change the repository unless the person explicitly asks.
2. **One area per run.** Agree on it with the person first (see the menu below). Read the whole area, not a grep sample.
3. **Evidence or it did not happen.** Every finding names the file and line, the input or state that triggers it, and
   what goes wrong. Where you can, prove it with a failing test (stdlib `testing`, in the package's `_test.go` style)
   and include the test in the report. Mark anything unproven as *plausible*, not *confirmed*.
4. **Security findings are private.** A vulnerability (auth bypass, SSRF, path traversal, injection, secret exposure,
   privilege escalation, ...) goes **only** into a private advisory: https://github.com/makearr/Solarr/security/advisories/new
   (see [SECURITY.md](../SECURITY.md)). Never into a public issue, never into the public report, not even as a hint.
5. **No secrets** of the person's in anything you write.
6. **Do not post anything yourself** unless asked. Print the commands; they run them.

## Step 1: orient

Read, in this order: `documentation/codebase-map.md` (the package tree), `documentation/coding-standards.md`,
`documentation/architecture.md`, `documentation/sql-and-sqlc.md` if the area touches SQL. Check the
[open issues](https://github.com/makearr/Solarr/issues) so you do not report a known problem.

Things the project does on purpose (not bugs): one writer per database; import concurrency 1; no transaction over
~50 ms, bulk work in 200-row batches; no `time.Now()` (a `clock.Clock` is injected), no direct `os` filesystem calls
(`fsx.FS`), no bare HTTP (`internal/platform/httpx`); generated code (`*.gen.go`, sqlc output) is never hand-edited, so a
bug there is a bug in `api/openapi.yaml` or `queries/`; links are reflink → hardlink → copy, never symlinks; nothing is
deleted directly (recycle bin first).

## Step 2: pick an area with the person

| Area | Where | Look especially for |
|---|---|---|
| Authentication and access | `internal/api/apiserver` (auth, middleware, per-library grants) | a route missing its access check, 403 where a 404 is required, session handling, proxy-header trust |
| Outbound requests | `internal/platform/httpx`, callers in `internal/metadata`, `internal/acquiring` | SSRF guard bypasses (redirects, DNS rebinding, IPv6, odd URL forms), credentials sent to another host |
| File operations | `internal/moving`, `internal/importing`, `internal/platform/fsx` | path traversal, deleting or overwriting outside the library, a source removed before the copy is verified, races |
| Import and identification | `internal/importing`, `internal/identify`, `internal/scanning` | wrong matches from edge cases, unbounded memory on huge folders, archive bombs |
| Download pipeline | `internal/acquiring` (planner, dispatch, clients) | duplicate grabs, lost state after a restart, blocklist holes, client answers trusted too much |
| Database layer | `queries/`, `internal/db` | missing indexes on hot paths, N+1 loops, long transactions, cursor paging without a tie-break |
| Jobs and scheduling | `internal/jobs` | lease and reaper races, jobs that can run twice, starvation |
| Web interface | `web/src` | XSS through provider data (titles, descriptions, HTML in overviews), unvirtualised long lists, request storms |
| Provider definitions | `internal/providerdef` | a YAML definition reaching hosts it did not declare, template injection, unbounded requests |

## Step 3: review

For each file in the area: what does it trust, what can a hostile input (a provider answer, a release name, a file name,
an archive, an HTTP request) make it do, what happens with 500,000 items instead of 5, what happens if the process dies
half way. Build and test through Docker, as the project does: `make test PKG=./internal/<area>/...` (needs only Docker
and make), `make race` for concurrency suspicions.

## Step 4: report

**Bugs and performance traps**: `solarr-review-<area>-YYYY-MM-DD.md`:

```markdown
## Code review: <area> (<commit sha>)

Reviewed: <files/packages>. Method: <read fully / ran tests / wrote N tests>.

### Findings (most severe first)
#### 1. <one-line summary>  [confirmed | plausible] [bug | performance]
- **Where:** `path/to/file.go:123`
- **Trigger:** <input or state>
- **What goes wrong:** <observed or expected wrong behaviour>
- **Evidence:** <failing test, measurement, or reasoning>
- **Suggested fix:** <short>

### Looked at and fine
<areas checked with nothing found: this is useful too>
```

Print, for the person to run:

```sh
gh issue create --repo makearr/Solarr --title "Code review: <area>" --body-file solarr-review-<area>-YYYY-MM-DD.md --label review
```

(Drop `--label` if it does not exist.)

**Security vulnerabilities**: a separate file `solarr-security-<area>-YYYY-MM-DD.md` with the same structure plus
impact (who can exploit it, from where, with what access) and a proof of concept if you have one. Tell the person to
submit it at https://github.com/makearr/Solarr/security/advisories/new (or with
`gh api repos/makearr/Solarr/security-advisories/reports` if they prefer the command line). Do not publish it anywhere else.
