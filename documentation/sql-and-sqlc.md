# SQL and sqlc

SQL lives in `queries/*.sql` and `migrations/*.sql`; typed Go is generated into
`internal/db/store/gen/` and committed (ADR-31). Change the SQL and run
`make sqlc`. CI fails on stale output.

## SQL files must be pure ASCII

Enforced by `TestSQLFiles_ContainNoMultiByteCharacters` in `internal/archtest`.

sqlc v1.31.1's SQLite engine miscounts statement boundaries when a file contains
a multi-byte character — **including inside a comment**. Every statement after
it shifts by one byte per multi-byte rune: it acquires a leading `;` and loses
its final character.

A single `§` in a header comment turned

```sql
SELECT id, username, ... FROM app_user ORDER BY username;
```

into generated Go containing

```sql
;

SELECT id, username, ... FROM app_user ORDER BY usernam
```

It compiles. It passes `sqlc diff`, because the committed output matches what
sqlc produces. It fails at runtime with `no such column: usernam`.

Nothing else in the build catches this, which is why it is a test and not a
convention. Write `section 5.5`, not `§5.5`, in SQL.

## What SQLite refuses

- **No expression in a `PRIMARY KEY` or `UNIQUE` constraint.** `PRIMARY KEY (a, b,
  COALESCE(c, ''))` fails with `expressions prohibited in PRIMARY KEY and UNIQUE
  constraints`. Make the column `NOT NULL DEFAULT ''` and key on it directly, which carries
  the same meaning.
- **NULL is admitted into a `PRIMARY KEY`.** SQLite does not enforce the standard's implied
  `NOT NULL`, so a key containing a nullable column stops deduplicating: two rows differing
  only by a `NULL` both insert. A column that scopes a key — `credit.part_id`, which is zero
  when the credit is on the work itself — must therefore be `NOT NULL DEFAULT 0` rather than
  nullable, which costs it its foreign key. The same reasoning made `credit.character`
  `NOT NULL DEFAULT ''`.

- **A foreign key cannot cross a database file.** `library.db` references `settings.db` rows
  by integer id with no constraint; the application enforces integrity (ADR-25). See
  `database-layout.md`.

- **No `ALTER TABLE` for a table's own constraint.** Widening or narrowing a `PRIMARY KEY`,
  `UNIQUE` or `CHECK` means rebuilding the table: create the new definition under a temporary
  name, copy every row across with an explicit column list (never `SELECT *`, same rule as
  above), `DROP` the old table, `ALTER TABLE ... RENAME` the new one into place, then recreate
  any indexes the drop took with it (they are not part of the table definition and do not
  survive).

  **The trap:** if any other table holds a live `ON DELETE CASCADE` or `ON DELETE SET NULL`
  foreign key into the one being rebuilt, `DROP TABLE` cascades those rows away the instant it
  runs — even though every id is about to be preserved in the replacement table under the same
  name. This is not a guess; it was confirmed with a throwaway script before writing migration
  0030 (`private/notes.md` §3, todo 4.23/F33). `PRAGMA foreign_keys=OFF` is the documented fix,
  but it is **also a no-op inside a transaction**, and `store.applyMigration` always runs a
  migration's SQL inside one. A migration that needs this puts
  `-- migrate:disable-foreign-keys` anywhere in its own `.sql` file (`migrations.go`'s own
  marker constant); `store.Migration.DisablesForeignKeys` then routes it through
  `runWithForeignKeysDisabled` instead of `runInTransaction` — the pragma toggles on `s.write`
  itself (safe only because that handle is pinned to exactly one connection,
  `SetMaxOpenConns(1)`) before `BEGIN` and after `COMMIT`, and `PRAGMA foreign_key_check` runs
  before the commit, refusing the migration outright rather than leaving a silent dangling
  reference. The marker lives inside the checksummed SQL text, so turning it on or off after the
  migration has applied is exactly the "changed migration" case `store.Migrate` already refuses.
  Migration 0030 (widening `desired_item`'s own key) is the first migration to need this.

## What sqlc refuses, or gets wrong quietly

- **No `SELECT *` or `RETURNING *`.** sqlc cannot expand them and fails with
  `extraneous input 'SELECTid'`. List columns explicitly, which is better practice anyway:
  adding a column then cannot silently reshape an existing result.

- **Column lists must match table order and be complete**, or sqlc stops reusing the table's
  model and generates a one-off row struct instead. This bites after `ALTER TABLE ADD COLUMN`:
  the new column has to be added to every `RETURNING` and `SELECT` that returned the whole row,
  in table order, or callers start seeing `InternAttributesRow` where they had
  `MediaAttribute`.

- **`sqlc.arg(name)` is not substituted inside `ON CONFLICT ... DO UPDATE SET`.** The literal
  text is emitted and SQLite rejects it with a syntax error near `(`. Use `excluded.<column>`,
  which refers to the row the insert was carrying:

  ```sql
  ON CONFLICT (scope, metric) DO UPDATE SET
    value = counter.value + excluded.value
  ```

- **Aggregates come back as `interface{}` without a cast**, because sqlc cannot infer the type.
  `COUNT(*)` is the exception and is inferred as `int64` on its own.

  ```sql
  SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS held FROM recycleBin
  ```

- **A recursive CTE must not name its column `id`**, or sqlc reports
  `column reference "id" is ambiguous` against the table being walked. Name it for the subject
  and qualify both sides:

  ```sql
  WITH RECURSIVE subtree(org_id) AS (
    SELECT organisation.id FROM organisation WHERE organisation.id = ?
    UNION
    SELECT o.id FROM organisation o JOIN subtree ON o.parent_id = subtree.org_id
  )
  ```

- **An argument that doubles as its own "unset" flag is not typed.** `sqlc.arg(x) = '' OR
  col = sqlc.arg(x)` can come back as `interface{}`. Split it into a flag and a value
  (`filter_by_state = 0 OR state = sqlc.arg(state)`, as `ListUnidentified` does) — and check the
  flag's polarity against the Go call, which a test of the filtered branch alone will not catch.

- **Several sibling subqueries in one `UPDATE ... SET` confuse the analyzer** into
  `column reference "session_id" is ambiguous`, although SQLite runs it fine. Alias the inner
  table in every subquery (`FROM import_unit iu WHERE iu.session_id = ...`).

- **`:copyfrom` is not supported for SQLite** (pgx and MySQL only). Bulk inserts are a per-row
  `:exec` looped inside one write transaction, as `ImportSessions.AddUnits` does.

- **Every branch of a `UNION` needs named columns**, or sqlc fails with
  `*ast.ResTarget has nil name`. Alias bare parameters and literals. Where a branch is only a
  constant row, a separate statement is usually clearer than an alias.

- **`table MATCH ?` — FTS5's whole-table match — does not parse.** sqlc's analyzer resolves
  `table` as a column reference and fails with `column "title_search" does not exist`. Match a
  specific column instead: `ts.title MATCH ?`. Harmless here since `title` is the FTS5 table's
  only indexed column anyway (everything else is `UNINDEXED`), so the two forms select
  identical rows — that stops being true the moment a second indexed column is added, at which
  point the whole-table form has no sqlc-compatible substitute and the query has to be
  hand-written outside `queries/`. The hidden `rank` column and `bm25()` both need the same
  `CAST(... AS REAL)` as any other aggregate, and an `UNINDEXED` column carries no declared
  type either, so an argument compared against it needs `CAST(sqlc.arg(x) AS TEXT)` or it comes
  back as `interface{}`. Worked example: `queries/library/search.sql`.

## What SQLite does not have

- **A `WITHOUT ROWID` table has no `rowid`.** `DELETE ... WHERE rowid IN (SELECT rowid ...)`
  fails against `metadata_cache`; use a row-value `IN` over its primary key instead:
  `WHERE (kind, external_id) IN (SELECT kind, external_id ... LIMIT ?)`.

## Conventions

- One file per domain area, named queries with `-- name: GetWorkByID :one`.
- `Queries.WithTx(tx)` binds to the write queue's transaction. Never open one
  directly (ADR-3).
- No `OFFSET`: cursor pagination only (Invariant 32).
- **A cursor needs a tie-break on `id`.** Ordering by a non-unique column alone silently drops
  rows when a page boundary falls inside a group that shares a value:

  ```sql
  AND (sqlc.arg(after_sort) = ''
       OR sort_title > sqlc.arg(after_sort)
       OR (sort_title = sqlc.arg(after_sort) AND id > sqlc.arg(after_id)))
  ORDER BY sort_title, id
  ```

- **A list surface composes its WHERE clause in Go, not in `queries/`.** This is the one
  place SQL is assembled rather than written, and the reason is measured: see below.
- **An insert on a table that grows with the library is `:execlastid`, not `:one`.** Returning
  the row costs about four times the insert itself, measured on both drivers; the column count
  is nearly free but the result set is not. A caller wanting the whole row reads it back.
  Numbers and method: `documentation/benchmarks.md`.
- No joins across database files. Compose in Go (architecture section 4.3).
- **Never write through the read pool inside a write transaction.** The write connection holds
  the lock, so the second connection blocks against it until the busy timeout expires.
  Everything inside a `store.Write` goes through the `Queries` it is given.

## Optional predicates, and where they are fatal

sqlc gives one static query, so an optional filter has to be written as a test on the
argument:

```sql
WHERE (sqlc.arg(media_type) = '' OR media_type = sqlc.arg(media_type))
```

**That predicate is not sargable.** SQLite cannot know at plan time that the argument is
non-empty, so it uses no index for that column. Measured against the 500k corpus, with
this as a list's only filter:

| Query | p50 | Plan |
|---|---|---|
| Optional predicate, one filter set | 6.75 ms | `SCAN work` + `USE TEMP B-TREE FOR ORDER BY` |
| Composed, one filter set | 0.22 ms | `SEARCH work USING INDEX work_type_sort` |

Thirty-one times, and the shape is worse than the ratio: the scan sorts the whole table
for every page, so it grows with the library rather than with the page.

**It is only fatal when it is the leading filter.** `FindWorksByNormalizedTitle` and
`FindWorksByClassification` keep theirs, because both drive off an indexed lookup first
and the optional `media_type` is a residual filter on the handful of rows that survive
it. Their plans were checked, not assumed.

So: a list surface — anything where the filters *are* the query — composes its WHERE in
Go with `store.Predicates`, adding only what is set. `internal/db/library/works.go` is the
worked example, and `ExplainList` beside it exists so a test can assert the plan rather
than the timing. Everything else stays sqlc.

**Assert plans through `store.ExplainQueryPlan`, never a bare `EXPLAIN QUERY PLAN`.** EXPLAIN
does not check the schema cookie, so a pooled connection that read the schema mid-migration
explains against indexes that no longer exist; the plan tests passed that way until 2026-09-28.
It indents each step by depth: a temp b-tree inside a correlated subquery is not the list's sort.

**A case-insensitive order needs a matching index.** `ORDER BY sort_name COLLATE NOCASE` is
served only by an index declared `COLLATE NOCASE` (migration 0046), and the cursor and prefix
comparisons must carry the same collation or pages disagree with the order.

- **A `sqlc.slice` used twice in one query is expanded once** (found 2026-10-15, a UNION over both directions of a
  relationship): give each use its own name (`part_ids`, `part_ids_again`) and pass the slice twice.
