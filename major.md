@corypride looks good, thanks. Both fixes are in: the D24 row is gone, the D5 row and its text now mention the requests, and the description matches. Approving.

**What changed around this PR**

While reviewing this, I checked what each site actually runs:
- **Only staging runs code from `main`.** Every other site file pins the legacy branch images (`demo`, `trial`, `alaska`, and so on). CI doesn't build those anymore, and they were built before EN-177, so they have no `/migrate`.
- **Preview** moved to the new chart in rev 5 but still runs the `demo` images. The app is healthy, because the legacy server still migrates at startup, but its `core-migrate-5` Job can never succeed. It also means a site can't move to the new chart on its legacy image: each move puts the site on a release built from `main`.
- **Some site branches have commits that aren't in `main`:** trial 12, alaska 5, maine 7, stlouis 1.
- **Preview and trial are treated like prod from now on,** so they need approval.

So I've re-planned the cutover:
- **EN-180 is now "Move demo onto the new setup".** It's still yours, and it's the next step after this PR. Demo moves to a pinned `main` release with the Open Content worker on, and it's the dress rehearsal for every later move, including a tested rollback runbook. Please start it after the bug bash. The steps are on the ticket.
- **Preview, trial and the prod sites each get their own ticket,** and they come last, after all the other refactor work (blocked by EN-208, EN-204 and EN-184):
  - EN-250 preview;
  - EN-251 trial;
  - EN-252 mocode (the prod canary);
  - EN-253 alaska, EN-254 stlouis and EN-255 maine.

  EN-249 audits the site branches' commits first.

**Please do now, before merging:**
- Take preview out of the deploy workflow's `DEPLOY_SITES` repo variable, so it's `staging` only and nobody redeploys preview by accident. You can do it under Settings → Secrets and variables → Actions → Variables, or with `gh variable set DEPLOY_SITES --repo UnlockedLabs/unlocked-platform --body "staging"`. Demo gets added when you set up its GitHub environment in EN-180.

**Please update the "After merge" section to:**
- Deploy staging only.
- Delete preview's Job that can't succeed: `kubectl -n preview delete job core-migrate-5`.
- Once staging's Kratos gives back about 450m of CPU, check that preview's `postgres-roles-5` and `hydra-migrate-5` complete, and that the node has CPU free for demo.
- Leave preview frozen on rev 5 until EN-250. Don't redeploy it from this chart on the `demo` image.
