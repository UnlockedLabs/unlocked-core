# Programs module

Programs, their classes and cohorts, enrollments, class events and
attendance, rooms, instructors, the Programs reports, and the Canvas and
Essential Education classes shown alongside them. It moved here from core in
EN-194.

## Boundaries

- **It imports nothing from core.** It talks to core only through
  `modules/module` (the seam), `unlocked-contracts` and Provider Platforms'
  `api/`. depguard in `backend/.golangci.yml` enforces this.
- **Core asks it questions through `api/`** (interfaces and plain types only),
  and gets the implementation from `registry.Programs`: the users list's
  `include=only_enrolled|only_unenrolled` filters, resident transfer and
  deactivation (on core's transaction, with `registry.ProgramsInTx`), the
  usage report, the dashboard's Programs numbers, and the facility list's,
  delete check's and account history's Programs data. Core never imports
  this package's insides.
- **It reaches core through `Deps`:**
  - `Deps.HTTP` for its Canvas and Essential Education calls;
  - `Deps.Features` to hide Canvas classes at facilities with Provider
    Platforms turned off;
  - `Deps.Reports` to render report PDFs with core's Jasper templates.
- **It answers Provider Platforms' `Deps.LiveProviders`**, through
  `registry.LiveProviders`: an Essential Education provider's users, and
  clearing the cached Canvas programs when a mapping changes. That goes away
  when Provider Platforms fetches and caches those providers' data itself.
- **It reads and writes some of core's tables:** it reads `users` and
  `facilities` (the `User` and `Facility` types in `models.go`), and adds
  rows to core's audit tables in the same transaction as its own changes:
  `user_account_history` when attendance is recorded, `change_log_entries`
  when a class or its schedule changes.
- **It asks Provider Platforms' `api/` on Provider Platforms' connection**,
  which the registry builds from `Deps.ModuleDBs`, so Programs' role needs no
  grants on provider tables.
- **Its routes keep core's access checks.** 35 of the 65 routes have a check
  of their own (`Route.Allow`), which `handlers.go` copies from core's
  resolvers.
- **Some code outside the module imports its insides:**
  - `backend/seeder`;
  - the integration tests, which build their data with its types and call its
    queries through `env.Programs` (the exported `Store`). depguard exempts
    `backend/tests/integration` for this.
- **Class activation** runs in the scheduler from `RegisterJobs` (`jobs.go`),
  every morning at 5 and once at start outside dev, with its queries in
  `program_classes_store.go`.

## Database

- **Its tables live in the `programs` schema** (EN-200). Its first migration
  moved all 14 there from `public`, with their sequences, and its rollback
  moves them back.
- **Its connection runs as the `programs` role**, which can read and write
  only this schema, read core's `users` and `facilities`, and read and add to
  (never change) core's `change_log_entries` and `user_account_history`.
  Postgres refuses anything else, so core asks for Programs' numbers through
  `api/` instead of reading its tables.
- **The role can't log in.** `Schema()` in `module.go` names the schema, and
  core opens this module its own pool as the app's usual database user
  (`database.OpenModuleDBs`). Each connection switches with
  `SET ROLE programs` and `search_path = programs, public`, so queries stay
  unqualified and no site needs a new password.
- **Core's transactions run Programs' part as the role.** Resident transfer
  and deactivation change Programs' enrollments inside core's transaction,
  through `registry.ProgramsInTx`, which wraps the call in `module.RunAs`
  (`SET LOCAL ROLE programs` and the search_path, then back to core's).
- **The history trigger** on `programs`, `program_classes` and
  `program_class_cohorts` is `programs.log_program_classes_updates()`, which
  writes `programs.program_classes_history`.
- **Later migrations must create tables as `programs.<name>`.** Module
  migrations run on core's connection, so an unqualified table lands in
  `public`, where the role can't use it. Tables created here get the role's
  grants automatically.
- **The enum types** (`class_status`, `credit_type`, `funding_type`,
  `program_type`) stay in `public`.
- **On SQLite** (the tests) there are no schemas or roles, so Programs uses
  core's connection.

## Files

| File | What it holds |
|---|---|
| `module.go` | Registers the routes and class activation, opens the Canvas cache buckets, lists the test tables, answers `Deps.LiveProviders` |
| `jobs.go` | Class activation |
| `handlers.go` | The routes, with their access levels, checks and feature flags |
| `programs_handler.go`, `program_class_tier_handler.go`, `classes_handler.go`, `class_events.go`, `class_enrollments.go`, `events_attendance_handler.go`, `reports_handler.go`, `facilities_handler.go`, `users_handler.go` | The handlers |
| `classes_service.go`, `users_service.go` | Class details, missing attendance and facility health; a resident's weekly attendance trend |
| `canvas_programs.go`, `canvas_classes_cache.go`, `canvas_events_cache.go`, `essential_ed_programs.go`, `live_provider.go` | The Canvas and Essential Education classes, read live and cached in NATS |
| `store.go` and `*_store.go` | Every database query, for the routes and the api |
| `api.go` | The answers to core's questions (`api.Programs`) |
| `program.go`, `program_classes.go`, `class_event.go`, `calendar.go`, `reports.go`, `change_log_entry.go`, `models.go` | Programs' tables and types, and the rows of core's tables it uses |
| `http.go` | Responses, errors, paging and request helpers, matching core's JSON |
| `utils.go` | Recurrence-rule helpers |
| `migrations/` | This module's own migrations: `00001` moves its tables into the `programs` schema and creates its role |

## Tests

- `backend/tests/integration/programs_routes_test.go` pins every route's
  status, JSON, access level and feature flag, the core routes that ask
  `api/`, and the Canvas and Essential Education paths through a TLS fake,
  against the saved responses in `testdata/programs/`. After an intended
  change, regenerate them from `backend/` with:

  ```bash
  go test ./tests/integration -run 'TestPrograms' -update
  ```

  The changed JSON then shows in the PR for review.
- `backend/tests/integration/programs_core_reads_test.go` pins the core
  routes that ask `api/` for Programs' data (the facility list and delete
  check, resident transfer, the account history), against
  `testdata/programs_core_reads/`.
- The other integration tests cover classes, events, attendance, conflicts,
  delete guards, reports and the Programs insights.
- The unit tests here cover the Canvas and Essential Education readers, the
  live providers, the calendar, enrollment checks, graduation and the PDF
  export.
- The tests run on SQLite without NATS, Canvas, Essential Education or
  Jasper, so they can't run the Postgres-only queries (pinned as 500s in the
  route tests), the NATS caches, the PDF rendering, or the schema and role.
  Check those with `make dev`.
