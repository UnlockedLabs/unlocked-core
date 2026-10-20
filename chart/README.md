# unlocked-core chart

Core's own Helm chart (EN-183). Standalone and self-contained: it has its own
`values.yaml` schema and its own trimmed `unlocked-core.*` named templates in
`_helpers.tpl` — it does not depend on, or share templates with,
`unlocked-platform`'s chart.

## What's in here

- `server-deployment.yaml` / `frontend-deployment.yaml` — the two Deployments,
  ported from `unlocked-platform`'s `charts/unlocked/templates/{server,frontend}-deployment.yaml`.
- `frontend-config.yaml` / `nginx-config.yaml` — the frontend's runtime config
  ConfigMap and nginx config.
- `persistent-volumes.yaml` — PV/PVC for the three volumes server+frontend
  use (`logs`, `thumbnails`, `jaspertmp`).
- `services.yaml` — ClusterIP Services for `frontend` and `server`.

This chart assumes it's deployed into a namespace that already has: a
`core-secrets` Secret (APP_KEY, DB_PASSWORD, ORY_TOKEN, NATS_PASSWORD), an
`ecr-secret` imagePullSecret, and `kratos`/`nats` Services reachable at the
hostnames in `values.yaml` — this chart does not create any of those. Today
that means deploying it standalone requires `unlocked-platform` to also be
installed in the same namespace for the in-cluster services it talks to.

## What's deliberately not in here yet

- **Migration-hook Job.** EN-177 — the ticket that builds the `/migrate`
  binary into the backend image and defines the `core-migrate-<revision>`
  Job pattern — hasn't merged yet
  ([unlocked-core#1258](https://github.com/UnlockedLabs/unlocked-core/pull/1258),
  [unlocked-platform#8](https://github.com/UnlockedLabs/unlocked-platform/pull/8)).
  Once it does, port the `core-migrate-<revision>` Job, the
  `core-migrate-waiter` RBAC, and the `wait-for-core-migrate` initContainer
  pattern from PR #8 into this chart.
- **Scheduler.** EN-191 ([unlocked-core#1257](https://github.com/UnlockedLabs/unlocked-core/pull/1257))
  runs background jobs as the same backend image invoked with
  `args: ["scheduler"]` instead of the server command, gated by a
  `RUN_SCHEDULER` env var (server keeps running jobs until that's `false`).
  Its own description scopes the actual chart Deployment for this as a
  separate follow-on PR after EN-179 lands — not bundled with EN-191 itself.
  Add a `scheduler-deployment.yaml` here once that's ready.

## Known gap: wiring this into unlocked-platform

This chart is independently `helm pull`/`helm template`-able today, but
`unlocked-platform` doesn't consume it yet. Two things still need a design
decision before that happens — see
`docs/plans/2026-10-20-en-183-platform-chart-dependency.md`:

1. How per-site values (`unlocked-platform/sites/<site>.yaml`) map onto this
   chart's flatter values schema when it's pulled in as a chart dependency.
2. The ECR chart repo this gets published to doesn't exist yet.

## Local verification

```bash
helm lint chart/ --set host=test.example.com \
  --set images.server.repository=x --set images.server.tag=x \
  --set images.frontend.repository=x --set images.frontend.tag=x \
  --set s3.bucket=x --set s3.region=us-west-2 --set kiwixServerUrl=http://x \
  --set volumes.logs.pvName=x --set volumes.logs.pvcName=x --set volumes.logs.hostPath=/x --set volumes.logs.size=1Gi \
  --set volumes.thumbnails.pvName=x --set volumes.thumbnails.pvcName=x --set volumes.thumbnails.hostPath=/x --set volumes.thumbnails.size=1Gi \
  --set volumes.jaspertmp.pvName=x --set volumes.jaspertmp.pvcName=x --set volumes.jaspertmp.hostPath=/x --set volumes.jaspertmp.size=1Gi

helm template unlocked-core chart/ [... same --set flags ...]
```
