## Pre-Submission PR Checklist

- [ ] No debug/console/fmt.Println statements
- [ ] Unnecessary development comments removed
- [ ] All acceptance criteria verified
- [ ] Functions according to **ticket specifications**
- [ ] Tested manually where applicable
- [ ] Branch rebased with latest main
- [ ] PR title and every commit are conventional commits (`feat:`, `fix:`, `docs:`...); a squash merge lands under the PR title
- [ ] No business logic exists within the database layer
- [ ] DB migrations (if any) are backward-compatible and online-safe — see [Database Migrations](https://github.com/UnlockedLabs/unlocked-core/blob/main/CONTRIBUTING.md#database-migrations) in CONTRIBUTING.md

## Description of the change

Please provide a brief description of the changes included in this PR.

- **Related issues**: Link to related JIRA ticket that this closes.

## Release

- **Changelog type**: `feat` (minor bump), `fix` (patch bump), or none (`chore`, `docs`, `ci`, `test`, `refactor`).
- **Breaking change?**: yes / no. If yes, add `!` to the title (`feat!:`) and list what consumers must change.

## Screenshot(s)

If the PR includes changes to the UI:

- Please add screenshots or a short screengrab of the change
- Include images at 1366 x 768 resolution for any Resident-facing changes.

## Additional context

Please include additional context or information that the reviewer needs to understand the PR. This includes:

- Known issues that the PR does not address
- What areas to focus on
- What decisions were considered and decided against.

If any core features or components were removed with this PR, please note them here so that they can be added to the wiki (see [Deprecated features and Components](https://github.com/UnlockedLabs/unlocked-core/wiki/Deprecated-Features-and-Components)).
