# EN-244 plan

## Survey (done by grep over all repos; complete sibling set)
unlocked-programs (9 files): models/program_classes.go (const x2 refs), models/program_classes_test.go, database/class_enrollments_store.go:276, database/program_classes_store.go:459, database/reports_store.go:20, database/seed_dev.go:69, handlers/internal_api_test.go (2), integration/class_enrollments_test.go:156, new migration 00004.
unlocked-core (3 files): frontend/src/types/attendance.ts:46, frontend/src/pages/admin/Exports.tsx:618,627, frontend/src/pages/ProgramsPage.tsx:1849.
Untouched deliberately: core migrations 00055, 00074; programs migration 00003. unlocked-platform, unlocked-contracts, open-content, provider-middleware: zero matches.

## Tasks (programs repo, then core)
1. programs: failing tests first (AC-2 literal assertion, AC-3 filter lookup), then rename constant + all references + roster key + migration 00004 (UPDATE Transfered -> Transferred; Down reverses). Sibling set: 9 files, all updated. Commit: `fix: spell Transferred correctly everywhere (EN-244)`. Checks: `go test -race ./...`, migration round trip.
2. core: rename enum member, Exports option and set, tooltip text. Sibling set: 3 files, all updated. Commit `fix(frontend): spell Transferred correctly (EN-244)`. Checks: yarn lint, yarn build, grep.

Commit order: programs first (backend accepts new label), core second. Each commit builds alone.
