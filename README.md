# UnlockEdv2

## Requirements

Currently, UnlockEdv2 is tested on Windows (WSL), Mac (homebrew) and Linux.

- Docker && Docker Compose
- Go 1.23
- Node.js > 18.0
- Yarn
- Make (optional, but recommended)

## Development

If you would like to contribute, please have a look at our [contribution guidelines](CONTRIBUTING.md).

> **Working on the AI Tutor (LLM) service?** See [`tutor-integration.md`](tutor-integration.md).
> The tutor is a separate app in the **sibling** repo `../ai/unlocked-hiset-ai`; compose builds
> it from that local path for `make dev` / `make dev-tutor`, so the two repos must sit side by
> side for those targets. `make dev-registry` pulls the tutor image from GHCR instead and needs
> no sibling checkout. That doc covers the rebuild loop (`docker compose up -d --build
> tutor-service`), enabling the `ai_tutor` feature flag, role-based access, SuperAdmin
> impersonation, and seeding demo data.

### Dependencies (Local)

**Please ensure you have the following installed properly before the further steps**

- Go 1.23

- Node.js >= v18.0

- Yarn 1.22.22

- Docker && Docker-Compose

### Dependencies (Deployment/Production)

- Node.js > 18.0
- Postgres 16.0
- NATS w/ `Jetstream`
- Ory: `Hydra` + `Kratos`

### Steps

- Clone the repository.

- `cp .env.example .env`.

- run `make init` to install dependencies, setup git hooks, and run the containers.

- run `make migrate` to run the initial migrations and populate database tables.

- Optionally, If you wish to seed the database with some basic test data, run `make seed`.


Subsequent runs can be done with `make dev`, which will start all the necessary containers
with hot reloading on the client and the backend.


**NOTE:** you _must_ be sure to use `127.0.0.1` in place of `localhost` in your browser, as the cookies required for authentication are not shared between the two,
and this can cause bad states in the browser that will prevent successful login/auth flow.

Login with `SuperAdmin` and password: `ChangeMe!`

You will be prompted immediately to set a new password and name for the default facility, and then you
will be redirected to the dashboard.


**Installing front-end dependencies**
Because `vite` runs in a docker container when developing, there is a docker volume with the `node_modules` mounted to the container. Installing the package in the frontend directory will not allow the changes to reflect when developing. The easiest way to do this is to run `make install-dep NAME=some_library` and it will take care of it for you.
If this does not work because your directory is named something different, you can do the following:

- bring down your containers (docker compose down)
- delete the node_modules volume `docker volume rm unlockedv2_node_modules` (will be called something different based on what your root directory is called)
- install the package with yarn in the frontend directory (`cd frontend && yarn add {pkg}`)
- bring the containers back up `make dev`

**Integrations:**

- _Kolibri_:
  If you wish to run + develop against `Kolibri` locally:
  first, you need to build and tag the docker image

  - `docker buildx build . -f config/kolibri.Dockerfile -t unlockedlabs.org/kolibri:latest`
  - Run `make kolibri`
    This will run all the containers, similarly to the 'prod' command but with the addition of the kolibri container,
    which will be available at `localhost:8000`

  The server will automatically create an OIDC client, which you can access either by logging in for the first
  time and going to `/api/oidc/clients` and adding the client_id and client_secret as environment variables in
  the `config/docker-compose.kolibri.yml` file. After you set this value, run `docker restart unlockedv2-kolibri-1`
  and you should be able to access the provider at `localhost:8000`

- _Canvas_:
  If you wish to run + develop against `Canvas` locally:

  - Run `make canvas`. That is the only command. It runs all the normal containers plus Canvas
    LMS, which gets its own postgres and redis so it never touches the unlocked database. Canvas
    will be available at `localhost:3001` (its postgres is published on `5433` to avoid clashing
    with ours on `5432`). On Apple Silicon this runs under emulation, because the `canvas-lms`
    image is only published for amd64. It works, but give it a few minutes to boot the first time.
  - **On the first run only**, `make canvas` also creates the Canvas database and seeds it with
    sample users, courses, enrollments, assignments and quizzes. This adds roughly 6-7 minutes,
    almost all of it Canvas migrations. Every run after that detects the existing database and
    skips straight to starting the containers. The Canvas admin it creates is
    `superadmin@unlocked.local` / `ChangeMe!` -- set `CANVAS_ADMIN_EMAIL` and
    `CANVAS_ADMIN_PASSWORD` if you want something else. Every seeded Canvas user has the password
    `password123`.
  - `make canvas-init` and `make canvas-seed` are still available for doing either step by hand --
    re-seeding, or re-creating the database after `docker compose ... down --volumes`.

  To connect UnlockEd to it, first run `make seed` if you have not already. `make canvas` seeds
  only the Canvas LMS database, so on a fresh UnlockEd database the `Canvas` provider platform the
  next step asks you to edit does not exist yet.

  Then log in to Canvas at `localhost:3001` as that admin, go to Account -> Settings ->
  New Access Token, and copy the token. Back in UnlockEd, enable the `provider_platforms` feature
  flag for your facility (every Canvas call is gated on it) and edit the seeded `Canvas` provider
  platform, pasting the token you just copied into `access_key`.

  That is the only field you need to fill in. `make seed` points the platform at the local
  Canvas (`base_url` `http://canvas`, `account_id` `1`) and leaves it enabled; the access key is
  the one value that can't be seeded, since you mint it per machine.

  **Note** the base url is `http://canvas`, not `localhost:3001`. UnlockEd calls the Canvas API
  from inside the `server` and `providerplatforms-worker` containers, so it needs the compose service
  name. `localhost:3001` only works from your browser.

 - **Videos**:
   Videos can be hosted locally for development, in which case they are simply downloaded and moved into the `frontend/public/videos` directory,
   If you wish to use `s3` and test out hosting videos between multiple instances, you need to set the following environment variables in either
   the docker-compose.yml for both the `opencontent-worker` and `server` or in your production environment.
 ```
     - AWS_REGION=
     - S3_BUCKET_NAME=
     - AWS_ACCOUNT_ID=
     - AWS_ACCESS_KEY_ID=
     - AWS_SECRET_ACCESS_KEY=
```
  You will also need to add the following section to `dev.nginx.conf` if you want to test s3 locally:

```
	location /videos/ {
		proxy_pass http://server:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
```

### To migrate the database to a fresh state, run `make migrate-fresh` (you can do this while docker is running with all the services, but you must restart the server (e.g. `docker restart unlockedv2-server-1` if your repo directory is called UnlockEdv2)



### **Quick fixes to common issues with development**

This product is under **active** development, and things are subject to abrupt change, frequently.
Here are a couple things to try if you are having errors with your local development environment:
If these don't work, look at the FAQ below, and if those don't work feel free to open an issue 👍

**First**, no matter the problem: try clearing your cookies in your browser. We rely on a csrf_token, an ory_session_token and an unlocked_token
to validate and authenticate our users. Clearing them and logging back in will often solve problems we have when developing
and creating many sessions and restarting the server.

**Second**: try migrating the database to a fresh state, and restarting docker.

**Third**: make sure your `.env` file is up to date. It's possible that you copied your .env.example over upon first cloning the repo,
so be sure that it didn't update in a later commit.

## Style/Linting

- Naming and style convention is outlined in our CONTRIBUTING.md file.

#### Proper linting/formatting _will_ run automatically in a git hook before each commit. If you want to run them beforehand, you can `cd` into frontend and run `npx prettier -w .` or `cd backend` and `gofmt -w .` IF for some reason you need to skip the pre-commit hooks, you can run `git commit --no-verify`but _do not_ do this unless you know what you are doing and you understand the CI/CD will fail if you submit a PR

## Releases

Versions come from [release-please](https://github.com/googleapis/release-please)
and our conventional commits (CI checks every commit subject and the PR title).

1. Merge PRs to `main` as usual (squash or rebase). A squash merge lands
   under the PR title, which is why CI checks it too. release-please keeps a
   `chore(main): release X.Y.Z` PR open with the next version and its
   `CHANGELOG.md` entry.
2. When it's time to release, review and merge that PR. It tags `vX.Y.Z` and
   creates the GitHub Release.
3. CI then builds `frontend` and `unlockedv2` at `X.Y.Z`
   in ECR. Staging keeps following `main`: every merge builds `sha-<commit>`
   images and CI sets staging's site file in `unlocked-platform` to them,
   which deploys staging (EN-184).

`feat` bumps the minor version, `fix` the patch, and a breaking change (`!`)
the major.

### Hotfixes

To patch a release without shipping unreleased work from `main`:

1. `git switch -c release/1.2.x v1.2.0`, then `git push origin release/1.2.x`.
2. Branch from `release/1.2.x`, commit the fix (`fix: ...`), and open a PR into
   `release/1.2.x`.
3. Merge it. release-please opens `chore(release/1.2.x): release 1.2.1`;
   merging that tags `v1.2.1` and builds the images. Release branches only
   ever bump the patch version.
4. If `main` needs the fix too, open a separate PR for it there.

Setup: `release-please-config.json` (`main`), `release-please-config.hotfix.json`
(`release/**`), `.release-please-manifest.json` and
`.github/workflows/release-please.yml`.

## FAQ/Troubleshooting

### I cannot log in with the default credentials or with the credentials I previously set

> This is likely an issue with the cookies that are set by our auth provider. Try the following steps:

- Clear your cookies in your browser
- Go to `127.0.0.1` in your browser (not `localhost`)
- Make sure you are clicking on the `Login` button on the welcome page, which should direct you to `127.0.0.1/self-service/login/browser`

- If you are still having issues, try restarting the server with `docker compose down --volumes` to clear the database
  before repeating the first 3 steps. Be sure that you are not using `localhost` in your browser,
  and that your URL has a ?flow={uuid} query parameter on the `/login` page.

### Why is docker not starting properly?

> Chances are, this is a permissions issue with docker. If you are new to docker, you may need to run `sudo usermod -aG docker $USER`
> to add yourself to the docker group. You will need to log out and back in for this to take effect.
> Try starting docker with `sudo dockerd`, or restarting the daemon with `sudo systemctl restart docker.service`, followed by `docker run hello-world`
> to ensure docker is running properly before again trying any of the `make` or `docker compose` commands.

### Docker says "network {UUID} not found"

run `docker compose up {-f docker-compose.yml} {-f config/docker-compose.fe-dev.yml | -f config/docker-compose.prod.yml} --build --force-recreate` depending on what services you need

# Debugging

Two tools you can use to aid in debugging:

- React Developer Tools. This is available for several browser flavors and available on their website: <https://react.dev/learn/react-developer-tools>

# License

UnlockEdv2 is open-sourced software licensed under the [Apache License, Version 2.0](https://opensource.org/license/apache-2-0/).
