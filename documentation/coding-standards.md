# Coding standards

Written for a codebase that will be maintained largely by AI agents working from a cold start.
Both goals point the same way: **predictable, explicit, boring code.**

---

## 1. Comments

**Comments are brief.** One line, occasionally two. They say *why*, never *what* — the code
already says what.

```go
// Immediate mode so a busy database blocks at BEGIN, not mid-transaction.
tx, err := db.BeginTx(ctx, &sql.TxOptions{})
```

```go
// BAD — restates the code, will rot
// Begin a transaction on the database using the provided context
tx, err := db.BeginTx(ctx, &sql.TxOptions{})
```

**If it needs a paragraph, it belongs in `documentation/`.** Link to the page by path:

```go
// Grammar and token precedence: documentation/release-name-grammar.md
func Parse(title string) Result {
```

Exported symbols get a doc comment in Go's standard form (`// Parse returns…`), kept to one
sentence where possible. Unexported code usually needs none.

Never leave commented-out code. Git remembers it. The same goes for a comment describing what
used to be there, or why an alternative was rejected — that is a design decision, and design
decisions live in `architecture.md`.

Three consecutive comment lines is always too many, in every file: Go, TypeScript, the
Dockerfile, the Makefile and the workflows. A bare `//` separator between paragraphs is the
clearest sign a comment has become an essay.

---

## 2. Naming

Names carry the explanation, so comments do not have to.

```go
// good
remainingWriteBudget, expiredLeaseCount, candidateRejectedNoSubtitles

// bad
rwb, n, flag, tmp, data, result2, doStuff
```

- No abbreviations beyond established ones (`ctx`, `db`, `id`, `url`, `tx`).
- Booleans read as assertions: `hasMBID`, `isDraining`, `acceptsNewFiles`.
- Functions with side effects are verbs; pure ones are nouns or `X-Of` forms.
- Loop variables may be short (`i`, `f`) only when the body is a few lines.
- **Be consistent across the codebase** — one concept, one word, everywhere. `candidate` is
  never also `release` or `item`. Grep is a primary navigation tool for both humans and
  agents; inconsistent vocabulary breaks it.

---

## 3. Structure

- **Small files, one concern each.** A file over ~400 lines is a signal to split.
- **Package by capability, not by layer.** `placement`, `parse`, `score` — never `utils`,
  `helpers`, `common`, `misc`. A package named for what it contains is findable.
- **Interfaces at the point of consumption**, defined by the consumer and kept small.
- **No hidden global state.** Dependencies arrive through constructors. Package-level `var`
  holding mutable state is banned.
- **Dependencies point one way** (`codebase-map.md`). A cycle is a design error;
  `internal/archtest` enforces it.
- Constructors take a struct when there are more than three parameters.

---

## 4. Explicit over clever

Clever code is expensive to maintain and hostile to an agent reading it cold.

- No reflection where a type switch or generics will do.
- No `interface{}` / `any` in domain code. Model the type.
- No stringly-typed values — `type Priority int`, not magic strings scattered about.
- No init-order dependencies beyond plugin registration.
- Errors are wrapped with context: `fmt.Errorf("resolve placement for %d: %w", id, err)`.
  Never discard an error silently; if it is genuinely ignorable, say so in a comment.
- One obvious way to do a thing. If there are two, delete one.

---

## 5. Repetition and generality

**Avoid unnecessary repetition — but generalise from evidence, not anticipation.**

- Two similar things: leave them. Three: extract the shared part.
- Prefer a general implementation *when the general case is already understood*. Indexers,
  downloaders and metadata providers are general because their shape is known. The plugin
  registry is deliberately extracted at stage 9, after two real domains exist — not before
  (ADR-4).
- When a media type needs something core cannot express, add a **named extension point that
  makes sense for every media type** (ADR-23) — never a branch in core, never an untyped
  escape-hatch column.
- Extensibility means the seam exists, not that every hook is pre-built.

---

## 6. Errors

Three classes, and the class decides behaviour (architecture §16):

| Class | Retry | Example |
|---|---|---|
| `Retryable` | yes, with backoff | timeout, 429, 503, `SQLITE_BUSY` |
| `Terminal` | **no** | parse failure, 404, permission denied |
| `Fatal` | no, alert | invariant violation, corrupt schema |

Classify at the point the error is created. Retrying a 404 spends an hour of backoff reaching
the same conclusion.

**Rejections and decisions are structured data, never formatted strings.** The UI formats;
the API filters; translation stays possible.

---

## 7. Tests

- **Standard library only.** No testify, no assertion DSL: `if got != want { t.Errorf(...) }`,
  with `reflect.DeepEqual` or small helpers local to the package for whole values. One idiom, no
  dependency in every test file, failure output under our control.
- Table-driven by default. Test names describe behaviour: `TestPlacement_PrefersAffinityRoot`.
- **Assert whole values, not fragments.** A scorer test asserts the full `Decision` including
  its components — a right answer for the wrong reasons is a bug when the reasons are the
  product.
- Use the seams: `clock.Clock`, `fsx.FS`, fake providers. **Never** `time.Now()`, never real
  network, never the real filesystem.
- Fakes model failure too: timeouts, 429s, malformed responses, partial results. Most bugs in
  this class of software live in error paths.
- Benchmarks live beside the code they measure and run in CI (ADR-28).
- **Assert the property, not the mechanism.** A test that a row round-trips proves the database
  works. A test that an extra does not satisfy a desired item proves the design works.
- **A vacuous pass is a failure.** Where a test could silently cover nothing — an empty table, a
  skipped branch, no routes walked — assert it found something before asserting anything about
  what it found.
- **Make the fixture disagree with what it asserts.** Ranks reversed against the expected
  order, timestamps identical across every row, a second copy of a part already covered. A
  fixture that happens to be in the right order tests nothing about ordering.
- Everything inside a `store.Write` goes through the `Queries` it is given. Writing through the
  read pool inside a write transaction blocks against the lock the same test is holding, until
  the busy timeout expires.

## 7a. Mutation testing

Before trusting a guard, break it and confirm a test fails. A guard nobody has watched fail is
a guard nobody knows works, and this has repeatedly found tests that passed for the wrong
reason:

- A fixture whose ordering coincided with the ordering under test, so a missing sort passed.
- A subject protected by something else entirely, so removing the guard under test changed
  nothing.
- A mutation that broke the build rather than the test, which proves nothing. Change the
  behaviour, not the signature — `AND x = ?` becomes `AND (x = ? OR 1 = 1)`, not a deleted
  parameter.

Restore the original immediately afterwards, and re-run to confirm green.

---

## 8. Making this easy for an AI to maintain

- **Convention over configuration.** The same concept lives in the same relative place in
  every package, so a file can be located by reasoning rather than searching.
- **Consistent vocabulary** (§2). One name per concept, everywhere.
- **Locality.** Everything needed to understand a function is in the file or one hop away. No
  behaviour assembled from five packages by indirection.
- **Types over comments.** `Confidence` and `Priority` cannot be misread the way `float64` and
  `int` can.
- **Small diffs.** One concern per commit. Mixed refactor-and-behaviour changes are hard to
  review and harder to revert.
- **Invariants stated in code**, not only in prose: a database constraint, a test, or a
  `panic` on a violated precondition beats a paragraph nobody reads.
- **No magic.** Code generation only where the generated output is committed and readable.
- Keep `documentation/` current in the same commit as the code it describes.

---

## 9. The stack, and how to work in it

Chosen so that no framework owns the shape of the code. Rationale in ADR-31.

| Layer | Choice |
|---|---|
| SQL | **sqlc** — SQL in `queries/*.sql`, typed Go generated into `internal/db/store/gen/` |
| Router | **stdlib `net/http` + chi** — everything stays an `http.Handler` |
| Logging | **`log/slog`** — JSON handler, correlation ID from context |
| API | **OpenAPI spec-first** — `api/openapi.yaml` generates Go interfaces and the TS client |
| Frontend | **Vite · React · TanStack Router/Query/Virtual · Tailwind · Radix primitives** |

### Generated code

`internal/db/store/gen/`, the Go API interfaces, and `web/src/api/` are **generated, committed,
and never edited by hand.** Change the source and regenerate:

```bash
make sqlc              # after changing queries/*.sql or the schema
make api               # after changing api/openapi.yaml
```

CI fails if generated output is stale. If a generated file looks wrong, the source is wrong.

### Writing queries

- One `.sql` file per domain area, named queries with `-- name: GetWorkByID :one`.
- Queries live in SQL, not in Go string literals. Reviewability is the point.
- `Queries.WithTx(tx)` binds to the write queue's transaction — never open your own (ADR-3).
- **No `OFFSET`.** Cursor pagination only.
- **No joins across database files.** Compose in Go (§4.3).

### Handlers

- Handlers unmarshal, call one service method, and marshal. No business logic, no SQL, no
  plugin calls (Invariant 22).
- The access context is threaded from middleware; **handlers never filter by category
  themselves** — the repository does (Invariant 36).
- Errors become RFC 7807 problem responses with a stable `type` slug.

### Logging

- `slog` with the request's logger from context, so correlation IDs thread automatically.
- Structured attributes, never formatted strings: `slog.Int("work_id", id)`, not
  `fmt.Sprintf`.
- **Never log a secret**, a full file path from user input, or an entire request body.
- Log at the point of handling, not at the point of raising — an error wrapped and returned is
  logged once, by whoever finally handles it.

### Frontend

- **Server state lives in TanStack Query.** Local component state is `useState`. There is no
  third category and no global store.
- SSE events invalidate query keys; the UI never polls.
- **Every list is virtualised.** 500k rows is the design point, not an edge case.
- Radix primitives are added per component as one is needed, styled with Tailwind
  (`web-interface.md` has why shadcn/ui was declined).
- TypeScript `strict` is on. `any` requires a comment justifying it.

---

## 10. Formatting and tooling

- `gofmt` (non-negotiable), `go vet`, `golangci-lint` — all enforced in CI.
- `CGO_ENABLED=0` must build (ADR-30).
- Line length is not enforced, but prefer breaking above ~110 characters.
- Import grouping: stdlib, external, internal — separated by blank lines.
