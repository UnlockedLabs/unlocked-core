# Learning Record module

The resident achievement journal: entries a resident records about a program
or activity, and the in-progress draft of one. It moved here from core in
EN-195.

## Boundaries

- **It imports nothing from core.** It talks to core only through
  `modules/module` (the seam) and `unlocked-contracts`. depguard in
  `backend/.golangci.yml` enforces this.
- **It owns no data but its own table** (`learning_record_entries`). An
  entry's `program_name` is free text, not a foreign key into Programs, so it
  has nothing to ask Programs' or Provider Platforms' `api/` packages today.
  The depguard rule for this module already allow-lists
  `modules/programs/api` and `modules/providerplatforms/api` for whenever
  that changes.
- **It reads core's `facilities` table** for the achievement-location picker
  (the `facility` type in `models.go`), the same way it reads `users` rows it
  doesn't own.
- **Core still owns** the `learning_record` feature flag
  (`models.LearningRecordAccess`). The `learning_record_deleted` audit action
  is this module's own private constant (`LearningRecordDeleted` in
  `models.go`), not an import of core's `models.LearningRecordDeleted` --
  depguard forbids that import, so it's duplicated like `UserAccountHistory`
  itself. Core's copy has no remaining consumers after the move.

## Files

| File | What it holds |
|---|---|
| `module.go` | Registers the routes, lists the test table |
| `handlers.go` | The routes, the 8 handlers, and location validation |
| `store.go` | The database queries the routes use |
| `api/` | The answers to core's questions (none yet, EN-186) |
| `models.go` | `LearningRecordEntry` and the local `facility`/audit-history types |
| `http.go` | Responses, errors and request helpers, matching core's JSON |
| `migrations/` | This module's own migrations (none yet; its table comes from core's `backend/migrations`) |

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
  `clause.OnConflict{..., Where: clause.Where{Exprs: []clause.Expression{
  clause.Expr{SQL: "deleted_at IS NULL"}}}}` in `store.go`'s `UpsertDraft`,
  plus swapping `gorm.Expr("NOW()")` for a portable `CURRENT_TIMESTAMP`
  (needed for the SQLite test, not for Postgres) — a follow-up ticket.
