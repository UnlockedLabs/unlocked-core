# EN-244 spec

Route: bug. Root cause: the transfer writer wrote 'Incomplete: Transferred'; the constant, outcomes query, roster filter and frontend used 'Transfered'. unlocked-programs 3e4bb6a (EN-207) already made the writer and a migration (00003) agree on the misspelling. Decision (user): the correct spelling 'Transferred' wins everywhere, so the typo stops being load-bearing.

## Ask
Spell it 'Transferred' in code, filters, frontend and stored rows, across unlocked-programs and unlocked-core.

## Acceptance criteria
- AC-1 (derived) Constant renamed to EnrollmentIncompleteTransferred = "Incomplete: Transferred"; no Go reference to the old name remains. Rung 1: `go build ./... && go test ./internal/models/...` plus grep for zero `Transfered`.
- AC-2 (derived) A transfer's enrollments appear in GetProgramClassOutcomes incomplete statuses. Rung 1: existing internal_api_test + integration test updated to the new constant; written value asserted as the literal string "Incomplete: Transferred" (new, fails first).
- AC-3 (derived) Roster filter label "Transferred" maps to "Incomplete: Transferred". Rung 1: new test on rosterStatusFilterMap/lookup, fails first.
- AC-4 (from ticket) Existing rows use one spelling. Rung 2: migration 00004 up+down round trip against a Postgres seeded with both spellings; no 'Transfered' rows after up. Rung 5 fallback only if no Postgres is reachable, stated.
- AC-5 (derived) Frontend enum, Exports filter option + NON_COMPLETING set, and ProgramsPage tooltip use 'Transferred'. Rung 3: `yarn lint && yarn build` plus grep for zero `ransfered` in frontend/src.
- AC-6 (derived) Filter selected in the Exports UI reaches the backend and returns the transferred enrollment. Rung 4: observed run if the stack can be started; otherwise UNVERIFIED.

Artifact/boundary: consumers are the Programs HTTP API (core frontend -> programs) and the programs DB schema. Checks sit at the store/handler layer in unlocked-programs and at the built frontend.

## Out of scope
- core migrations 00055, 00074 (already applied history; rewriting them is wrong).
- programs migration 00003 (already applied; stays, 00004 supersedes it).
- Any other typo hunt, renaming Go identifiers beyond this one.
- Deploy sequencing automation.

## Open questions / answers
- Repo: clone unlocked-programs (done, ../unlocked-programs).
- Spelling: Transferred wins (user, after being told programs main already standardized on Transfered).
- Risk accepted: frontend and programs must deploy together; an old client sending 'Transfered' to the new backend gets an unmatched filter.
