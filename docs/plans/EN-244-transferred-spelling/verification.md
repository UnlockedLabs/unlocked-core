# EN-244 verification

| AC | Rung | Check | Result | Evidence |
|----|------|-------|--------|----------|
| AC-1 | 1 | `go test ./internal/models` (TestTransferredStatusSpelling) | PASS | failed to compile before the rename (undefined constant), passes after; grep `ransfered` in programs internal finds only migration 00004's WHERE clause and 00003 history |
| AC-2 | 1 | `go test ./internal/handlers` (transfer asserts the constant, now "Incomplete: Transferred"), outcomes query uses same constant | PASS | full `go test -race ./...` green |
| AC-3 | 1 | `go test ./internal/database -run TestMapRosterStatusesTransferred` | PASS | failed first: got "Transferred", want "Incomplete: Transferred" |
| AC-4 | 2 | 00004 UPDATE run on scratch Postgres 16 with both spellings | PASS | UPDATE 1; result 2 Transferred, 1 Enrolled. Down is a no-op by design, so no round trip |
| AC-5 | 3 | `yarn lint && yarn build`; grep `ransfered` frontend/src | PASS | lint clean, build ok, zero matches |
| AC-6 | 4 | Exports roster filter in a browser | UNVERIFIED | full stack not started here; the integration of label 'Transferred' -> map key is covered by AC-3 |

Not run: /defend-rfc, /pr-review-uel, /codex.
Accepted on faith: core's old public.program_class_enrollments table (00074 backfilled it as 'Transfered') was moved into the programs schema by programs 00001; not checked against a live DB.
Rollout: frontend and unlocked-programs must deploy together; an old client's 'Transfered' filter label matches nothing on the new backend.
Left alone on purpose: core migrations 00055/00074, programs 00003, stale spelling in docs/plans/2026-09-23-en95-*.md.
