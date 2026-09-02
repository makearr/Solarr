# SQL and sqlc

SQL lives in `queries/*.sql` and `migrations/*.sql`; typed Go is generated into
`internal/store/gen/` and committed (ADR-31). Change the SQL and run
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
  SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) AS held FROM quarantine
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

- **Every branch of a `UNION` needs named columns**, or sqlc fails with
  `*ast.ResTarget has nil name`. Alias bare parameters and literals. Where a branch is only a
  constant row, a separate statement is usually clearer than an alias.

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

- **An insert on a table that grows with the library is `:execlastid`, not `:one`.** Returning
  the row costs about four times the insert itself, measured on both drivers; the column count
  is nearly free but the result set is not. A caller wanting the whole row reads it back.
  Numbers and method: `documentation/benchmarks.md`.
- No joins across database files. Compose in Go (architecture section 4.3).
- **Never write through the read pool inside a write transaction.** The write connection holds
  the lock, so the second connection blocks against it until the busy timeout expires.
  Everything inside a `store.Write` goes through the `Queries` it is given.
