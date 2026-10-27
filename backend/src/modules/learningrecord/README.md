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
  (`models.LearningRecordAccess`) and the `learning_record_deleted` audit
  action's string literal (`models.LearningRecordDeleted`), the same split
  every other module's feature flag and audit actions use.

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
- `PUT /api/learning-record/draft` (the upsert) isn't covered by a golden
  case: it sets `updated_at` via `gorm.Expr("NOW()")`, valid Postgres but not
  SQLite, so every call 500s under this harness. Pre-existing, not introduced
  by the move — check it with `make dev`.
