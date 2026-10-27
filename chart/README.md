# unlocked-core chart

Core's own Helm chart (EN-183). Standalone and self-contained: it has its own
`values.yaml` schema and its own trimmed `unlocked-core.*` named templates in
`_helpers.tpl` — it does not depend on, or share templates with,
`unlocked-platform`'s chart.

## What's in here

- `server-deployment.yaml` / `frontend-deployment.yaml` — the two Deployments,
  ported from `unlocked-platform`'s `charts/unlocked/templates/{server,frontend}-deployment.yaml`.
- `scheduler-deployment.yaml` — runs background jobs (EN-191: same backend
  image, `args: ["scheduler"]`). Always `replicas: 1` and `strategy: Recreate`,
  not a values knob. `tasks.Start()` has no leader election, so two scheduler
  pods running at once would double-publish every job to NATS; `Recreate`
  only closes that window during a rollout (a template change), not a node
  failure or drain — on those, Kubernetes can start a replacement pod while
  the original's status is still unknown. That gap is a known risk, not yet
  closed; closing it for real means leader election in `tasks.Start()` or a
  NATS dedupe ID, not a chart-side setting.
  `server-deployment.yaml` always sets `RUN_SCHEDULER=false` to match: this
  chart never runs jobs in both places.
- `frontend-config.yaml` / `nginx-config.yaml` — the frontend's runtime config
  ConfigMap and nginx config.
- `persistent-volumes.yaml` — PV/PVC for the three volumes server+frontend
  use (`logs`, `thumbnails`, `jaspertmp`). The scheduler and migrate Job
  need none of these.
- `services.yaml` — ClusterIP Services for `frontend` and `server`. Neither
  the scheduler nor the migrate Job has a Service — nothing calls into them.
- `migrate-job.yaml` — a per-revision `core-migrate-<revision>` Job that runs
  `/migrate -dir /migrations` (EN-177, merged in
  [unlocked-core#1258](https://github.com/UnlockedLabs/unlocked-core/pull/1258)/
  [unlocked-platform#8](https://github.com/UnlockedLabs/unlocked-platform/pull/8)).
  Ported from platform's `core-migrate-job.yaml` after its own review
  rounds simplified it away from RBAC and a server-side "wait for this Job"
  initContainer: the server and scheduler each wait on migration state
  themselves (`database.WaitForMigrations`) instead, so nothing needs to
  coordinate with this Job directly — it only has to run.

This chart assumes it's deployed into a namespace that already has: a
`core-secrets` Secret (APP_KEY, ORY_TOKEN, NATS_PASSWORD, and either
DB_PASSWORD or APP_DSN depending on `database.useAppDsn`), an
`ecr-secret` imagePullSecret, and `kratos`/`nats` Services reachable at the
hostnames in `values.yaml` — this chart does not create any of those. Today
that means deploying it standalone requires `unlocked-platform` to also be
installed in the same namespace for the in-cluster services it talks to.

One required-key difference from `unlocked-platform`'s Secret: there,
`APP_KEY` is `optional: true` (prod intentionally has none). Here it's
required — a deliberate fix for a fail-open encryption-key gap (a missing
key silently encrypted data under a known constant). A site reusing an
existing `core-secrets` Secret that has no `APP_KEY` entry must add one
(an explicit empty string preserves today's behavior) before this chart's
server will start.

Not included: the suspended staging CronJob variant platform also renders
alongside its Job — that's a workaround for staging's non-Helm restart path
specific to that repo (gone once EN-184 lands there), not a concern of this
chart.

## Publishing

`charts/unlocked-core` exists in ECR (`239442854891.dkr.ecr.us-west-2.amazonaws.com/charts/unlocked-core`,
created 2026-10-26 — this is the first chart ever published to this account's
ECR; confirmed with Rich, nothing else publishes one today). The
`GithubActionsBuildPushEcr` role `chart_release.yml` assumes already has
push access — its policy is a wildcard on `repository/*`, not scoped to
specific repos, so no IAM change was needed for this new one.

## Known gap: wiring this into unlocked-platform

This chart is independently `helm pull`/`helm template`-able today, but
`unlocked-platform` doesn't consume it yet — that's separate, tracked work
(EN-183) for that repo, not designed here.

## Local verification

```bash
FLAGS="--set host=test.example.com \
  --set images.server.repository=x --set images.server.tag=x \
  --set images.frontend.repository=x --set images.frontend.tag=x \
  --set s3.bucket=x --set s3.region=us-west-2 --set kiwixServerUrl=http://x \
  --set volumes.logs.pvName=x --set volumes.logs.pvcName=x --set volumes.logs.hostPath=/x --set volumes.logs.size=1Gi \
  --set volumes.thumbnails.pvName=x --set volumes.thumbnails.pvcName=x --set volumes.thumbnails.hostPath=/x --set volumes.thumbnails.size=1Gi \
  --set volumes.jaspertmp.pvName=x --set volumes.jaspertmp.pvcName=x --set volumes.jaspertmp.hostPath=/x --set volumes.jaspertmp.size=1Gi"

helm lint chart/ $FLAGS
helm template unlocked-core chart/ $FLAGS
```
