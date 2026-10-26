# Open Content module

Libraries (Kiwix), videos (YouTube), helpful links, favorites, reading
activity and the content proxies. It moved here from core in EN-192.

## Boundaries

- **It imports nothing from core.** It talks to core only through
  `modules/module` (the seam) and `unlocked-contracts`. depguard in
  `backend/.golangci.yml` enforces this.
- **Core asks it questions through `api/`** (interfaces and plain types only),
  and gets the implementation from `registry.OpenContent`. Core never imports
  this package's insides.
- **It runs its own jobs** (EN-196). `RegisterJobs` schedules `scrape_kiwix`,
  `retry_video_downloads` and `sync_video_metadata`, and the worker,
  `backend worker opencontent`, runs them along with the two video jobs the
  routes publish (`add_videos`, `retry_manual_download`). The worker needs
  yt-dlp (with node as its JavaScript runtime) and ffmpeg, which the backend
  image ships.
- **Core still owns** the websocket, which Open Content messages through
  `Deps.Notify`.

## Database

- **Its tables live in the `open_content` schema** (EN-198). Its first
  migration moved all 11 there from `public`; its rollback moves them back.
- **Its connection runs as the `open_content` role**, which can read and write
  only this schema and read only core's `users` and `facilities`. Postgres
  refuses anything else, so Open Content can't reach into core's tables, and
  core asks for Open Content's numbers through `api/` instead of reading its
  tables.
- **The role can't log in.** `Schema()` in `module.go` names the schema, and
  core opens this module its own pool as the app's usual database user
  (`database.OpenModuleDBs`). Each connection switches with
  `SET ROLE open_content` and `search_path = open_content, public`, so queries
  stay unqualified and no site needs a new password.
- **Tables a later migration adds here** get the role's grants automatically.
- **On SQLite** (the tests) there are no schemas or roles, so Open Content
  uses core's connection.

Provider Platforms (EN-199) and Programs (EN-200) follow the same pattern:
return the schema's name from `Schema()`, and have the module's first
migration create the schema and its role and move the tables.

## Files

| File | What it holds |
|---|---|
| `module.go` | Registers the routes and jobs, runs the worker, sets up AWS and the `library_paths` cache, seeds a new database, lists the test tables |
| `handlers.go` | The API routes, with their access levels and feature flags |
| `proxy.go` | The library proxy (to Kiwix) and the video proxy (to S3) |
| `service.go` | Facility visibility, recording visits, the content-request email, the library cache |
| `store.go` | Every database query, for the routes, the api and the jobs |
| `api.go` | The answers to core's questions (`api.OpenContent`) |
| `models.go` | Open Content's tables as Go types |
| `jobs.go` | The scheduled jobs and their schedules, and the job messages for the worker |
| `worker.go` | The worker: which job runs which code, and each job's time limit |
| `kiwix.go`, `kiwix_data.go` | The Kiwix scrape: reads the catalog, saves each library and its thumbnail |
| `video_dl.go` | Video downloads with yt-dlp, retries, and the metadata sync with the videos folder |
| `http.go` | Responses, errors, paging and request helpers, matching core's JSON |
| `migrations/` | This module's own migrations. The first moves its tables into the `open_content` schema and creates its role |

## Tests

- `backend/tests/integration/open_content_routes_test.go` pins every route's
  status, JSON, access level and feature flags against the saved responses in
  `testdata/open_content/`. After an intended change, regenerate them from
  `backend/` with:

  ```bash
  go test ./tests/integration -run 'TestOpenContent' -update
  ```

  The changed JSON then shows in the PR for review.
- `kiwix_test.go`, `video_dl_test.go` and `worker_test.go` run the jobs
  against a fake Kiwix, a fake core upload endpoint and a fake `yt-dlp` on
  `PATH`, so they need no network.
- The tests run on SQLite without Kiwix, NATS or AWS, so they can't run
  search (Postgres full-text search and Kiwix), the library proxy forwarding
  to Kiwix, the content-request email (SES), the jobs going through NATS, or
  the schema move and the role (`TestAllModulesMigrate` only checks that its
  migrations load). Check those with `make dev`.
