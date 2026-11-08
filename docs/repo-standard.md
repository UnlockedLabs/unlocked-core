# Repository standard

Every UnlockEd repo meets this standard. The reference is `unlocked-hiset-ai` (set up in EN-182). The suite today: `unlocked-core`, `unlocked-platform`, `unlocked-contracts`, `golang-jasper`, `unlocked-hiset-ai`, the product repos made from the template (`unlocked-provider-platforms`, `unlocked-programs`, `unlocked-learning-record`, `unlocked-open-content`), and the template `unlocked-product-template`. New product repos (EN-202) are created from that template; see the checklist at the end.

## The standard

### Ruleset
A ruleset named `protect-main` on the default branch. No human can bypass it; a GitHub App may be listed when a pipeline needs it, and each one is recorded under Named bypass exceptions:

- Restrict deletions and block force pushes.
- Require a PR with 1 approval, dismiss stale approvals, require approval of the most recent push, and require conversation resolution.
- Required status checks: the repo's tests and lint, `conventional commits`, and `pr title`.
  A check that only runs for some file paths is never required, or it blocks every other PR.
- Merge, squash and rebase are all allowed.

The classic branch protection is removed once the ruleset replaces it. Two protections on one branch is how settings drift.

### CI checks
- Tests and lint for the repo's language.
- `conventional commits`: every commit on the branch matches `type(scope)!: subject`. Merge commits are skipped.
- `pr title`: the PR title matches the same pattern. A squash merge lands a multi-commit PR under its title, and a non-conventional title breaks release-please's changelog.
- Third-party actions are pinned by SHA: `uses: owner/repo@<40-hex> # <tag>`. Resolve with `gh api repos/<owner>/<repo>/commits/<tag> --jq .sha`, which gives the commit, not an annotated tag object.
- Allowed types: `feat fix docs style refactor perf test build ci chore revert`.
- Every repo runs the same inline commit check on `pull_request`, using the PR's base and head SHAs and skipping merge commits. Copy the step from any repo's workflow; do not use a third-party action for it. GitHub's Revert button writes `Revert "..."`, which the check rejects, so edit the message to a conventional one such as `revert: ...`.

### Files
- `.github/PULL_REQUEST_TEMPLATE.md` with a JIRA link, a checklist, and a Release section.
- `.github/CODEOWNERS` containing `* @UnlockedLabs/reviewers`. More specific rules may follow it; the last matching line wins.

### CodeRabbit
CodeRabbit reviews every PR. It is a GitHub app installed per repo by an org admin, with no config file in the repo. Reviewing private repos needs a CodeRabbit plan that allows them.

## Per repo

| Repo | Ruleset id | Required checks | Branches |
|------|-----------|-----------------|----------|
| unlocked-core | 25118710 | `tests`, `golangci-lint (/home/runner/work/unlocked-core/unlocked-core/backend)`, `Lint and build JS`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH`, `refs/heads/release/**` |
| unlocked-platform | 25506255 | `lint and render every site`, `plan`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |
| unlocked-contracts | 25024361 | `vet and test`, `golangci-lint`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |
| golang-jasper | none yet | `conventional commits`, `pr title` (it has no tests or lint) | `~DEFAULT_BRANCH` |
| unlocked-hiset-ai | 25090572 | `test`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |
| unlocked-product-template | 26150292 | `vet and test`, `golangci-lint`, `govulncheck`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |
| unlocked-provider-platforms | 26559590 | `vet and test`, `golangci-lint`, `govulncheck`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |
| unlocked-programs | 26622764 | `vet and test`, `golangci-lint`, `govulncheck`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |
| unlocked-learning-record | 26559589 | `vet and test`, `golangci-lint`, `govulncheck`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |
| unlocked-open-content | 26392199 | `vet and test`, `golangci-lint`, `govulncheck`, `conventional commits`, `pr title` | `~DEFAULT_BRANCH` |

Core's golangci-lint check name contains the runner path; it changes if the repo or its backend directory is renamed.

## Applying the ruleset

Needs an org admin token, and `bash` (the helpers below are not valid fish). Without admin, the API hides `bypass_actors`, and the helper refuses to continue.

### Existing rulesets: read, change only the checks, diff, then PUT
A `PUT` replaces the whole ruleset, so never build one from a template for a repo that already has one. That would drop its bypass actors, extra rules and branch targets. This helper changes only the required check names and leaves everything else as it is live:

```sh
set -o pipefail
strip='del(.id,.node_id,.source,.source_type,.created_at,.updated_at,._links,.current_user_can_bypass)'
stage() { # stage <repo> <ruleset id> '<required checks, JSON array>'
  gh api repos/UnlockedLabs/$1/rulesets/$2 > $1.before.json || return
  jq -e 'has("bypass_actors")' $1.before.json > /dev/null \
    || { echo "token cannot see bypass_actors, need org admin"; return 1; }
  jq -S "$strip
    | (.rules[] | select(.type==\"required_status_checks\") | .parameters.required_status_checks)
        = (\$c | map({context: ., integration_id: 15368}))" --argjson c "$3" $1.before.json > $1.after.json
  diff <(jq -S "$strip" $1.before.json) $1.after.json; [ $? -le 1 ]   # diff exits 1 when there are changes, which is expected
}
put() { gh api -X PUT repos/UnlockedLabs/$1/rulesets/$2 --input $1.after.json; }   # put <repo> <id>
```

Run `stage`, read the diff (it should show only check names), then `put`. Integration id 15368 is GitHub Actions.

```sh
stage unlocked-core 25118710 '["tests","golangci-lint (/home/runner/work/unlocked-core/unlocked-core/backend)","Lint and build JS","conventional commits","pr title"]'
stage unlocked-platform 25506255 '["lint and render every site","plan","conventional commits","pr title"]'
stage unlocked-contracts 25024361 '["vet and test","golangci-lint","conventional commits","pr title"]'
stage unlocked-product-template 26150292 '["vet and test","golangci-lint","govulncheck","conventional commits","pr title"]'
stage unlocked-provider-platforms 26559590 '["vet and test","golangci-lint","govulncheck","conventional commits","pr title"]'
stage unlocked-programs 26622764 '["vet and test","golangci-lint","govulncheck","conventional commits","pr title"]'
stage unlocked-learning-record 26559589 '["vet and test","golangci-lint","govulncheck","conventional commits","pr title"]'
stage unlocked-open-content 26392199 '["vet and test","golangci-lint","govulncheck","conventional commits","pr title"]'
# after reading each diff:  put <repo> <id>
```

Apply each after that repo's PR merges, so the new checks exist. unlocked-core, unlocked-product-template and the four product repos made from it are the exception: they still require the old name `conventional`, see below. Check names must match the job `name:` exactly.

### Named bypass exceptions
No human can bypass `protect-main`. A GitHub App may be listed when a pipeline needs it, and every exception is recorded here. The `stage` flow above keeps them because it never rewrites the bypass list.

| Repo | Exception | Why |
|------|-----------|-----|
| unlocked-platform | `unlocked-release-bot` app | unlocked-core's `container_builds.yml` (`update-staging`) pushes image-tag updates straight to platform's `main`; platform's deploy workflow checks that push changes only tags |

### New repos and golang-jasper: create from the template
Only for a repo with no ruleset yet. Save the reference ruleset as `protect-main.json`:

```json
{
  "name": "protect-main",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": { "ref_name": { "exclude": [], "include": ["~DEFAULT_BRANCH"] } },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": true,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["merge", "squash", "rebase"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": false,
        "do_not_enforce_on_create": false,
        "required_status_checks": []
      }
    }
  ]
}
```

```sh
set -o pipefail
gh api repos/UnlockedLabs/golang-jasper/rulesets --jq '.[].name'   # must print nothing, or a second protect-main gets created
jq --argjson c '["conventional commits","pr title"]' \
   '(.rules[] | select(.type=="required_status_checks") | .parameters.required_status_checks) = ($c | map({context: ., integration_id: 15368}))' \
   protect-main.json > jasper.json
gh api -X POST repos/UnlockedLabs/golang-jasper/rulesets --input jasper.json
```

The template keeps only the documented `pull_request` parameters. It has not been tried against the live API from this checkout; if GitHub rejects it, copy the working parameters from the `unlocked-hiset-ai` ruleset.

### Repos still on `conventional`: swapping it for `conventional commits`
unlocked-core, unlocked-product-template and the four product repos made from it called the job `conventional`, and their live rulesets still require that name. The template's ruleset is also what new repos copy, so swap it before creating the next repo. The template has no release branches; skip the first check there. Switching the ruleset early blocks every other open PR, which still reports the old name. Delete this section once the swap is done.

Before starting, check both of these. Either can still require the old name:
```sh
gh api repos/UnlockedLabs/unlocked-core/branches --paginate --jq '.[].name' | grep '^release/'
gh api repos/UnlockedLabs/unlocked-core/branches/main/protection --jq '.required_status_checks.contexts'
```
The ruleset also covers `release/**`. Any live release branch still reports `conventional`, so backport the rename to it first, or its PRs block.

1. Once every check on the EN-214 core PR has passed except `conventional` (pending until the swap) and it is approved, run `stage unlocked-core 25118710 ...` and `put unlocked-core 25118710` from above.
2. Merge the PR straight away.
3. Tell authors of open core PRs to rebase onto main so they report the new name.

The template and the four product repos the same way: once each repo's EN-214 PR has passed every check except `conventional` and is approved, `stage`/`put` its ruleset, merge, and ask authors of its open PRs to rebase. None of them has release branches. A release-please PR picks up the new name the next time release-please updates it after a push to main.

### Removing classic protection
Do this only after the ruleset is live and verified, so the branch is never unprotected. Use the repo's real default branch, not an assumed `main`:
```sh
b=$(gh repo view UnlockedLabs/<repo> --json defaultBranchRef -q .defaultBranchRef.name)
gh api repos/UnlockedLabs/<repo>/branches/$b/protection        # 404 means none, or no permission
gh api -X DELETE repos/UnlockedLabs/<repo>/branches/$b/protection
```
A 404 on the first call also appears when the token lacks admin, so check in Settings > Branches if unsure.

### Verifying
With an admin token:
```sh
gh api repos/UnlockedLabs/<repo>/rulesets/<id> --jq '{bypass: (.bypass_actors | if type=="array" then map(.actor_type + ":" + (.actor_id|tostring)) else "hidden, token lacks admin" end), checks: [.rules[]|select(.type=="required_status_checks")|.parameters.required_status_checks[].context]}'
```
`bypass` must list only the exceptions in the table above (an empty list everywhere else). Without admin the API omits `bypass_actors`, so it reports `hidden`; that proves nothing. (A plain `.bypass_actors|length` prints `0` for a hidden field, which is why the command tests the type.)

## CodeRabbit install
1. Upgrade the CodeRabbit plan if it does not cover private repos.
2. GitHub org settings > Installed GitHub Apps > CodeRabbit > Configure, then add `unlocked-platform`, `unlocked-contracts`, `golang-jasper` and any of `unlocked-provider-platforms`, `unlocked-programs`, `unlocked-learning-record`, `unlocked-open-content` not already listed (hiset-ai has it; confirm core on the same page).
3. Open a PR in each repo and confirm CodeRabbit comments on it.

## New repo checklist (EN-202)
A Go product repo: create it from `unlocked-product-template` and follow the "New product checklist" in its README, which covers everything below and copies the template's ruleset. Any other repo:

- [ ] `.github/PULL_REQUEST_TEMPLATE.md` with a JIRA link, checklist and Release section.
- [ ] `.github/CODEOWNERS` with `* @UnlockedLabs/reviewers`.
- [ ] Workflows for tests, lint, `conventional commits` and `pr title`; every third-party action pinned by SHA.
- [ ] Merge the first PR, then create the `protect-main` ruleset from the section above with this repo's check names. No classic protection.
- [ ] Install CodeRabbit on the repo.
- [ ] Do not require any check that is path-filtered.
