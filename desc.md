# Running and testing the learning platform integrations

Two providers are read live from their APIs and projected as synthetic programs and classes:
**Canvas** and **Essential Education**. Neither is synced into our tables.

Canvas setup already lives in `README.md` (the `- _Canvas_:` bullet) — start there for Canvas.
This file covers Essential Education, which is not documented anywhere else, and adds the Canvas
gotchas the README does not mention.

---

## Essential Education

### There is nothing to run

Unlike Canvas, Essential Ed has **no container, no `make` target and no seed**. It is a hosted
sandbox, so the entire setup is pointing UnlockEd at it: a URL and a token. Don't go looking for
a compose service.

There is also no migration — `provider_platforms.type` is a plain `varchar(100)`, so
`essential_ed` needs nothing added.

### 1. Enable the feature flag

Enable `provider_platforms` for your facility. Every live-provider call is gated on it, and so is
the sidebar item, so the page below is invisible without it.

### 2. Add the platform

**Sidebar → Learning Platforms → Add Learning Platform**

| Field | Value |
| --- | --- |
| Name | anything — it becomes the program name (use `Essential Ed`) |
| Platform Type | **Essential Education** (last of the four options) |
| Provider Instance URL | `https://api.essentialed.com/v20181129` |
| Access Token | the sandbox token — **ask Rich** |

Then **Add Platform**.

Two things to get right on the URL:

- **No trailing slash.**
- **No leading or trailing whitespace.** Pasting a URL with a leading space used to produce
  `first path segment in URL cannot contain colon` on every call. The value is trimmed on save and
  on read now, but it is still the sort of thing worth looking at twice.

The token is sent as the `X-API-Token` header, not a bearer token
(`essential_ed_programs.go`, `essentialEdTransport.authorize`).

> **TODO:** where to mint a fresh token in the Essential Ed console
> (`app.essentialed.com/manage/unlocked-labs-api-test-district` → ?). Don't reuse an old one that
> has been passed around.

### 3. Map users — do not skip this

The Add dialog gets you a connection, but **every class will read 0 enrolled until residents are
mapped to Essential Ed users.** `provider_user_mappings` starts empty, and the enrollee counters
intersect the provider's students against that empty set. This looks exactly like a broken
integration and is not one.

**Learning Platforms → click the platform row → Manage Users**

Matching runs automatically when the page loads — there is no "match" button to find. **Refresh**
re-runs it. You get three buckets:

- **Auto-confirmed** — matched on name similarity. Confirm each.
- **Needs review** — possible matches. Confirm, or pick a different resident.
- **Unmatched** — no match. **Create** a resident, or **Link existing**.

Per row: **Confirm**, **Link existing**, **Create**. In bulk: **Confirm all** on Auto-confirmed,
**Confirm all suggestions** on Needs review. When you're done, **Apply N matches** →
**Apply matches**.

### 4. Check it worked

1. **Programs** page lists the Essential Education program next to Canvas.
2. Open it — classes are **College Prep I** and **High School Prep I**.
3. Log in as a resident you mapped — **My Programs** shows those classes.

### Sandbox reference

- District **303**; schools **6592** and **6593**.
- 3 students: `student1` / `student2` / `student3`, ids **4329946–48**.
- 2 classes, both in school 6592: **College Prep I** (69742) and **High School Prep I** (69743).
  Student1 and Student2 are in both; Student3 is in neither, so a resident mapped to Student3
  correctly shows nothing.
- **Classes can only be created in the web console.** There is no `POST /classes` — the API
  creates and updates *users* only.

### Known, expected, not your bug

- **Three nightly jobs fail.** The scheduler builds `get_courses`, `get_milestones` and
  `get_activity` for Essential Ed because the platform is enabled, and all three fail: there is no
  Essential Ed sync in the Provider Platforms worker, by design. The worker logs
  `unsupported provider type: essential_ed` and leaves the task marked running. They only fire on
  `MIDDLEWARE_CRON_SCHEDULE` (default `0 22 * * *`).
- **`account_id` is wrong.** The Add dialog hardcodes `account_id: '1'` and neither dialog exposes
  a field for it. It should be the district, `303`. Nothing reads it today — the class URL is just
  `base_url + "/classes"` and the token's own scope decides which schools come back — so it is
  wrong data rather than a broken behaviour.

---

## Canvas — FYI

Full instructions are in `README.md`. The short version:

- `make canvas` — the only command. Runs the normal stack plus Canvas LMS, which gets its own
  postgres and redis and never touches the unlocked database. Canvas lands on `localhost:3001`.
- **First run only** also creates and seeds the Canvas database: roughly 6–7 extra minutes, almost
  all of it Canvas migrations. Later runs skip it.
- Canvas admin `superadmin@unlocked.local` / `ChangeMe!`; every seeded Canvas user is
  `password123`.
- To connect: run `make seed` first if you have not — `make canvas` seeds only the Canvas LMS
  database, so without it the `Canvas` provider platform you are about to edit does not exist.
  Then in Canvas, Account → Settings → New Access Token, copy it, edit the seeded `Canvas`
  platform in UnlockEd and paste it into **Access Token**. That is the only field you need —
  `make seed` sets the rest.

**The one people get wrong:** `base_url` is `http://canvas`, *not* `localhost:3001`. The API is
called from inside the `server` and `providerplatforms-worker` containers, so it needs the compose service
name. `localhost:3001` only works from your browser.

### Gotchas not in the README

- **Log in at `http://127.0.0.1`, not `localhost`** — cookie/auth issue. Sessions also expire; if
  the UI behaves strangely, grep the server log for `Ory session not active` before debugging
  anything else.
- **Canvas tokens are per-machine and expire.** A stale one shows up as
  `canvas API returned 401` in the server log while the platform still looks correctly configured
  and every call quietly fails. Worth checking first if Canvas data is missing.
- **A panic in the server does not restart it in dev** and `RestartCount` stays 0, because
  `server` runs under `air`, which doesn't bring the inner process back. Grep the logs
  for `panic` instead of trusting container state.
- **gopls goes stale** and will report compiler errors that aren't real. Trust `go build` over the
  editor; restart the language server if it gets confused.
