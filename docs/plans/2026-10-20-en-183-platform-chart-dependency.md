# EN-183 (unlocked-platform): pin unlocked-core as a chart dependency

> **Status:** not started. This repo's half of EN-183 (the standalone
> `chart/` in `unlocked-core`, plus the release-publish CI step) is done —
> see `chart/README.md`. This doc records what's left, so it isn't lost.

**Depends on / coordinates with:** this is the `unlocked-platform` side of
EN-183, the same split `unlocked-core`'s EN-177 plan used with
`unlocked-platform`'s EN-177 plan (`docs/plans/2026-10-16-en-177-stop-startup-migrations.md`).

## What's blocking this

1. **An ECR repo to pull the chart from.** `chart_release.yml` pushes to
   `oci://<ecr>/charts/unlocked-core` on release, but that ECR repo doesn't
   exist yet — confirmed by searching every repo in the `UnlockedLabs` GitHub
   org; `unlocked-infra`'s Terraform has `aws_ecr_repository` resources for
   the three image repos (`unlockedv2`, `provider_middleware`, `frontend`)
   but none for charts. The user (CK-7vn) said they can create this
   themselves — do it before trying step 1 below, or the `helm dependency
   build` / `helm pull` in every step after will fail with "repository not
   found."
2. **The migration-hook Job and scheduler aren't in `unlocked-core`'s chart
   yet**, because the PRs that define their shape haven't merged:
   - EN-177: [unlocked-core#1258](https://github.com/UnlockedLabs/unlocked-core/pull/1258) (core), [unlocked-platform#8](https://github.com/UnlockedLabs/unlocked-platform/pull/8) (platform) — both CI-green, awaiting review, as of 2026-10-20.
   - EN-191: [unlocked-core#1257](https://github.com/UnlockedLabs/unlocked-core/pull/1257) — has a failing "Scan Go Dependencies for Vulnerabilities" check and open review comments as of 2026-10-20. Its own description scopes the scheduler's chart Deployment as a separate follow-on PR after EN-179, not bundled with EN-191.

   Don't start this plan until at least EN-177 (both PRs) has merged and
   `unlocked-core`'s chart has grown the migration-hook Job (see
   `chart/README.md`'s "deliberately not in here yet" section) — otherwise
   the platform chart would regress: it already runs `core-migrate-<revision>`
   via PR #8's pattern, and switching to a dependency that doesn't have that
   yet would silently drop it.

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

3. **Add the dependency to `Chart.yaml`**, same pattern as the existing
   `nats`/`kratos` dependencies (EN-211/EN-212):
   ```yaml
   dependencies:
     - name: unlocked-core
       version: "X.Y.Z"  # pinned per release, same as nats/kratos
       repository: "oci://<ecr>/charts"
   ```

4. **Verify output is unchanged for staging**: `scripts/render.sh staging`
   before and after, diffed (the repo already has `scripts/manifest-diff.py`
   for this — used for the EN-177 platform work). This is the ticket's own
   Done-when criterion: "The platform chart's output for staging is unchanged
   after the switch." Budget time for this to take a few passes — Helm
   dependency value-passing bugs usually show up as a diff, not an error.

## Not part of this plan

- Creating the ECR chart repo itself — the user is handling that directly,
  not through this plan.
- Any change to `unlocked-core`'s chart beyond what EN-177/EN-191 add — this
  plan only consumes what that chart publishes, it doesn't design it further.
