# Learning Record module

The resident achievement journal: entries a resident records about a program
or activity, and the in-progress draft of one. It moved here from core in
EN-195.

## Boundaries

- **It imports nothing from core.** It talks to core only through
  `modules/module` (the seam) and `unlocked-contracts`. depguard in
  `backend/.golangci.yml` enforces this.
- **It owns no data but its own table** (`learning_record_entries`, in the
  `learning_record` schema; see Database). An
  entry's `program_name` is free text, not a foreign key into Programs, so it
  has nothing to ask Programs today. Provider Platforms (EN-206) and Programs
  (EN-207) are their own services, so they aren't modules here.
- **It reads and writes some of core's tables:** it reads `facilities` for the
  achievement-location picker (the `facility` type in `models.go`), and adds a
  `user_account_history` row when a resident deletes an entry, in the same
  transaction. It touches no other core table; the caller's identity comes from
  the request claims, not from `users`. Its role can do only those two things
  with core's tables (see Database).
- **Core still owns** the `learning_record` feature flag
  (`models.LearningRecordAccess`). The `learning_record_deleted` audit action
  is this module's own private constant (`LearningRecordDeleted` in
  `models.go`), not an import of core's -- depguard forbids that import, so
  it's duplicated like `UserAccountHistory` itself.

## Database

- **Its table lives in the `learning_record` schema** (EN-286). Its first
  migration moved `learning_record_entries` there from `public`; its rollback
  moves it back. Core's migrations 00069 and 00073 still create the table in
  `public` on a new database, and this module's migrations run after core's.
- **Its connection runs as the `learning_record` role**, which can read and
  write only this schema, read core's `facilities`, and insert into core's
  `user_account_history` (plus `USAGE` on its id sequence). Postgres refuses
  anything else.
- **The role can't log in.** `Schema()` in `module.go` names the schema, and
  core opens a pool for this module as the app's usual database user
  (`database.OpenModuleDBs`). Each connection switches with
  `SET ROLE learning_record` and `search_path = learning_record, public`, so
  queries stay unqualified and no site needs a new password.
- **Later migrations must create tables as `learning_record.<name>`.** Module
  migrations run on core's connection, so an unqualified table lands in
  `public`, where the role can't use it. Tables created here get the role's
  grants automatically.
- **On SQLite** (the tests) there are no schemas or roles, so Learning Record
  uses core's connection.

## Files

| File | What it holds |
|---|---|
| `module.go` | Registers the routes, lists the test table |
| `handlers.go` | The routes, the 8 handlers, and location validation |
| `store.go` | The database queries the routes use |
| `api/` | The answers to core's questions (none yet, EN-186) |
| `models.go` | `LearningRecordEntry` and the local `facility`/audit-history types |
| `http.go` | Responses, errors and request helpers, matching core's JSON |
| `migrations/` | This module's own migrations. The first moves its table into the `learning_record` schema and creates its role |

## Tests

- `backend/tests/integration/learning_record_routes_test.go` pins every
  route's status, JSON, access level and feature flag against the saved
  responses in `testdata/learning_record/`. After an intended change,
  regenerate them from `backend/` with:

  ```bash
  go test ./tests/integration -run 'TestLearningRecord' -update
  ```

  The changed JSON then shows in the PR for review.
- `PUT /api/learning-record/draft` (the upsert) is pinned as a 500: its
  `ON CONFLICT` target doesn't match migration 00069's partial unique index
  on `(user_id, client_id) WHERE deleted_at IS NULL`, and Postgres won't
  infer a partial index for a target that doesn't repeat its predicate.
  Confirmed against a live Postgres container, not just this SQLite
  harness — a pre-existing production bug, not introduced by the move, and
  not fixed here (out of scope for a move). The fix is
  `clause.OnConflict{..., TargetWhere: clause.Where{Exprs: []clause.Expression{
  clause.Expr{SQL: "deleted_at IS NULL"}}}}` in `store.go`'s `UpsertDraft` --
  `TargetWhere` repeats the index's predicate on the conflict target itself;
  `Where` instead conditions the `DO UPDATE`, which doesn't match the index
  either (confirmed against a live Postgres container: it fails a different
  way, `42702` ambiguous column, not `42P10`). Also swap `gorm.Expr("NOW()")`
  for a portable `CURRENT_TIMESTAMP` (needed for the SQLite test, not for
  Postgres) — a follow-up ticket.
