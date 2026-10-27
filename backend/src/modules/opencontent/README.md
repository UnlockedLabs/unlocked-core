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
- **Core still owns:**
  - the websocket, which Open Content messages through `Deps.Notify`;
  - three plain SQL reads of Open Content's tables (facility list, facility
    delete check, user engagement), until EN-198.

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
| `migrations/` | This module's own migrations (none yet; its tables come from core's) |

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
  to Kiwix, the content-request email (SES) or the jobs going through
  NATS. Check those with `make dev`.
