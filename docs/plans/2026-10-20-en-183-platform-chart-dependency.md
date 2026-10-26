# EN-183 (unlocked-platform): pin unlocked-core as a chart dependency

> **Status:** not started. This repo's half of EN-183 (the standalone
> `chart/` in `unlocked-core`, plus the release-publish CI step) is done —
> see `chart/README.md`. This doc records what's left, so it isn't lost.

**Depends on / coordinates with:** this is the `unlocked-platform` side of
EN-183, the same split `unlocked-core`'s EN-177 plan used with
`unlocked-platform`'s EN-177 plan (`docs/plans/2026-10-16-en-177-stop-startup-migrations.md`).

## What's blocking this

~~1. An ECR repo to pull the chart from~~ — done 2026-10-26.
   `charts/unlocked-core` exists in ECR
   (`239442854891.dkr.ecr.us-west-2.amazonaws.com/charts/unlocked-core`,
   confirmed with Rich: core is the first chart this account has ever
   published, so the naming (`charts/<chart-name>`, one repo per chart, not
   one shared `charts` repo) is the pattern future product charts — tutor's
   EN-203 next — will copy). `unlocked-infra`'s Terraform is not what
   manages the real ECR repos in this account (verified directly: the real
   `frontend`/`unlockedv2`/`provider_middleware` repos predate that repo's
   last commit by a year, have no tags, and don't match its naming scheme —
   they were created by hand), so this one was created the same way, by
   hand, for consistency with how every other repo here actually exists.
   Also confirmed: the `GithubActionsBuildPushEcr` role `chart_release.yml`
   assumes already has push access (wildcard `repository/*` policy) — no
   IAM change was needed.

~~2. The migration-hook Job and scheduler aren't in `unlocked-core`'s chart yet~~
   — done. EN-177 merged on both sides 2026-10-22
   ([unlocked-core#1258](https://github.com/UnlockedLabs/unlocked-core/pull/1258),
   [unlocked-platform#8](https://github.com/UnlockedLabs/unlocked-platform/pull/8)),
   and EN-191's scheduler ([unlocked-core#1257](https://github.com/UnlockedLabs/unlocked-core/pull/1257))
   and its gate EN-179 both merged the same week. `unlocked-core`'s chart now
   has `migrate-job.yaml` and `scheduler-deployment.yaml` — see
   `chart/README.md`'s "What's in here" section for what each does.

## The actual integration work, once unblocked

1. **Values translation.** `unlocked-core`'s chart has its own flat values
   schema (`chart/values.yaml`): `host`, `images.server.*`, `database.*`,
   `s3.*`, etc. `unlocked-platform`'s chart gets its values from
   `sites/<site>.yaml` under a different, more nested schema
   (`global.host`, `images.frontend.repository`/`.tag`, `database.mode`,
   etc. — see `unlocked-platform/charts/unlocked/values.yaml`). Helm passes
   values to a chart dependency three ways:
   - a `unlocked-core:` key in the parent's values (platform would need a
     per-site block translating its schema into core's, which is
     duplicative — most site files already set these values once for
     platform's own templates)
   - `global:` values, visible to the parent and every dependency (works for
     the handful of truly shared values like `host`, but core's schema has
     more fields than just the globals platform already defines)
   - `import-values` in `Chart.yaml`, which can map specific parent keys into
     a dependency's values at specific paths — probably the best fit here,
     since it avoids a second copy of every value, but needs someone to map
     every field core's chart requires

   Recommend: write out the exact `import-values` mapping against one real
   site file (`sites/staging.yaml`) before touching anything, and check the
   `unlocked.validate` named template (`_helpers.tpl`) still makes sense once
   `database.mode`/`useAppDsn` live on the platform side only.

2. **Replace `charts/unlocked/templates/{server,frontend}-deployment.yaml`,
   `frontend-config.yaml`, `nginx-config.yaml`** (and the `server`/`frontend`
   entries in `persistent-volumes.yaml` and `services.yaml`) with the
   `unlocked-core` dependency. `services.yaml` currently generates every
   component's Service from one shared template keyed off `.Values.components.*`
   — removing `server`/`frontend` from that list without breaking hydra/
   kolibri/tutor/postgres needs care, since it's one combined loop, not
   one file per component.

   `unlocked-core`'s chart now also ships `scheduler-deployment.yaml` and
   sets `RUN_SCHEDULER=false` unconditionally on `server`. Platform never
   needs its own scheduler Deployment — it comes along free with the
   dependency.

   Also replace `charts/unlocked/templates/core-migrate-job.yaml` with the
   dependency's `migrate-job.yaml` — same Job name pattern
   (`core-migrate-<revision>`), so nothing downstream that references it by
   name needs to change. Keep platform's suspended staging CronJob variant
   (`migrations.restartTemplate` in that file) if staging still needs it at
   cutover time — `unlocked-core`'s chart deliberately doesn't render that
   piece, since it's a staging-specific restart-path workaround, not
   something every consumer of this chart should carry. `postgres-roles-job.yaml`/
   `postgres-grants-job.yaml` stay in platform's chart untouched — they set up
   hydra/kratos/kolibri/tutor roles, not anything core-specific.

3. **Add the dependency to `Chart.yaml`**, same pattern as the existing
   `nats`/`kratos` dependencies (EN-211/EN-212):
   ```yaml
   dependencies:
     - name: unlocked-core
       version: "X.Y.Z"  # pinned per release, same as nats/kratos
       repository: "oci://239442854891.dkr.ecr.us-west-2.amazonaws.com/charts"
   ```
   (Helm appends the dependency's own `name` to that path, resolving to the
   same `charts/unlocked-core` repo `chart_release.yml` pushes to.)

4. **Verify output is unchanged for staging**: `scripts/render.sh staging`
   before and after, diffed (the repo already has `scripts/manifest-diff.py`
   for this — used for the EN-177 platform work). This is the ticket's own
   Done-when criterion: "The platform chart's output for staging is unchanged
   after the switch." Budget time for this to take a few passes — Helm
   dependency value-passing bugs usually show up as a diff, not an error.

## Not part of this plan

- Any change to `unlocked-core`'s chart beyond what EN-177/EN-191 add — this
  plan only consumes what that chart publishes, it doesn't design it further.
