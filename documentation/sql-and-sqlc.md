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

## Other constraints of the SQLite engine

- **No `SELECT *` or `RETURNING *`.** sqlc cannot expand them and fails with
  `extraneous input 'SELECTid'`. List columns explicitly, which is better
  practice anyway: adding a column then cannot silently reshape an existing
  result.

## Conventions

- One file per domain area, named queries with `-- name: GetWorkByID :one`.
- `Queries.WithTx(tx)` binds to the write queue's transaction. Never open one
  directly (ADR-3).
- No `OFFSET`: cursor pagination only (Invariant 32).
- No joins across database files. Compose in Go (architecture section 4.3).
