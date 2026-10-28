# Provider Platforms module

LMS connections (Canvas, Brightspace, Kolibri, Essential Education), the users
linked to them, their courses, outcomes and activity, and the OIDC clients that
let residents log in to them. It moved here from core in EN-193.

## Boundaries

- **It imports nothing from core.** It talks to core only through
  `modules/module` (the seam) and `unlocked-contracts`. depguard in
  `backend/.golangci.yml` enforces this.
- **Core asks it questions through `api/`** (interfaces and plain types only),
  and gets the implementation from `registry.ProviderPlatforms`. Core never
  imports this package's insides.
- **It reaches core through `Deps`:**
  - `Deps.Users` gets, creates and deletes users and gives them a Kratos
    login;
  - `Deps.LiveProviders`, which Programs answers (EN-194), lists an Essential
    Education provider's users and clears Programs' cached Canvas programs
    when a mapping changes. It goes away when this module fetches and caches
    those providers' data itself.
- **It reads core's `users` table** to match and list the users linked to a
  provider (the `user` type in `models.go`).
- **`backend/seeder` still imports its insides.** depguard doesn't check it:
  `golint.yml` skips the seeder.
- **Programs shows the Canvas and Essential Education live data** on its
  screens, asking `api/` about providers and the users linked to them.
- **Its LMS jobs** (`get_courses`, `get_milestones`, `get_activity`) are
  scheduled by `RegisterJobs` (`jobs.go`) and run by
  `backend worker providerplatforms` (`worker.go`), which also serves the
  provider user lists the server asks for on `:8081` (`PROVIDER_SERVICE_URL`).
  It keeps the jobs' `cron_jobs`/`runnable_tasks` rows through its own types,
  and `runnable_tasks.provider_platform_id` stays a foreign key into
  `provider_platforms`.
- **Core still owns** creating a user with provider accounts
  (`POST /api/users`), the unmapped-users list
  (`GET /api/users?include=only_unmapped`), the admin dashboard's course
  numbers and the demo seed, which ask `api/`.

## Files

There's no `service.go`: the logic is split by topic (`users.go`,
`matching.go`, `accounts.go`).

| File | What it holds |
|---|---|
| `module.go` | Registers the routes and the LMS jobs, starts the worker, sets up the `oauth_state` bucket, lists the test tables |
| `handlers.go` | The routes, with their access levels and feature flags, and the provider, OAuth, OIDC and learning-data handlers |
| `users.go` | Logins and mappings, importing users and matching them to residents |
| `matching.go` | The name matching behind the matching screen |
| `accounts.go` | Creating a user's Canvas or Kolibri account and Canvas login |
| `jobs.go` | Schedules each active provider's LMS jobs and publishes them for the worker |
| `worker.go` | The worker: runs the LMS jobs and serves the provider user lists |
| `canvas.go` | Canvas's sync |
| `brightspace.go`, `brightspace_data.go` | Brightspace's sync, from its data set downloads |
| `kolibri.go`, `kolibri_data.go` | Kolibri's sync; its queries against Kolibri's own database stay here |
| `providerservice.go` | The client for the worker's user lists |
| `store.go` | The database queries the routes and the LMS sync use |
| `api.go` | The answers to core's questions (`api.ProviderPlatforms`) |
| `models.go` | Provider Platforms' tables as Go types |
| `http.go` | Responses, errors, paging and request helpers, matching core's JSON |
| `migrations/` | This module's own migrations (none yet; its tables come from core's until they move to its own `provider` schema in EN-199) |

## Tests

- `backend/tests/integration/provider_platforms_routes_test.go` pins every
  route's status, JSON, access level and feature flag, and the core routes
  that ask `api/`, against the saved responses in
  `testdata/provider_platforms/`. Fakes stand in for the worker's user lists,
  Kratos, Hydra and Kolibri. After an intended change, regenerate them from
  `backend/` with:

  ```bash
  go test ./tests/integration -run 'TestProviderPlatform' -update
  ```

  The changed JSON then shows in the PR for review.
- The unit tests here cover `api.go` (on SQLite), the name matching, the
  provider user lists and the LMS jobs: each provider's sync on SQLite, with
  the Postgres procedure calls recorded, plus the worker and the scheduling.
- The tests run on SQLite without NATS, Canvas, Brightspace or Essential
  Education, so they can't run the Brightspace OAuth flow, creating Canvas
  accounts and logins, or seeding demo data (which calls Postgres
  procedures). Check those with `make dev`.
