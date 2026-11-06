package integration

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/coreapi"
	programsapi "github.com/UnlockedLabs/unlocked-contracts/programs"
)

// fakePrograms stands in for the Programs service (EN-207). Core asks it over
// HTTP, through the same client and handler as in production, and it answers
// with what the test sets. Unset answers are empty, as a site with no Programs
// data answers.
type fakePrograms struct {
	mu sync.Mutex
	// enrolled and eligible are each class's users.
	enrolled, eligible map[uint][]uint
	transferConflicts  []programsapi.ResidentTransferProgramConflicts
	facilityPrograms   map[uint]programsapi.FacilityPrograms
	classHistoryAt     map[uint]time.Time
	engagement         programsapi.ProgramEngagementOverview
	secondEnrollment   []programsapi.SecondProgramEnrollmentRow
	completionMatrix   []programsapi.ProgramCompletionMatrixCell
	loadDistribution   programsapi.ProgramLoadDistribution
	enrollmentByType   []programsapi.ProgramTypeEnrollment
	classMetrics       programsapi.ClassDashboardMetrics
	facilityHealth     []programsapi.FacilityHealthSummary
	// failChanges makes ending and withdrawing enrollments fail.
	failChanges bool
	// ended and withdrawn are the enrollment changes core asked for, in order.
	ended     []endedForTransfer
	withdrawn []uint
}

// endedForTransfer is one EndEnrollmentsForTransfer call.
type endedForTransfer struct {
	userID, fromFacilityID uint
}

var _ programsapi.Programs = (*fakePrograms)(nil)

// fakeProgramsToken is the site's secret in core's tests.
const fakeProgramsToken = "test-programs-token"

// startFakePrograms serves a fakePrograms and points PROGRAMS_URL and
// PROGRAMS_TOKEN at it, for the test server core builds next.
func startFakePrograms(t *testing.T) *fakePrograms {
	t.Helper()
	fake := &fakePrograms{}
	ts := httptest.NewServer(programsapi.NewHandler(fake, fakeProgramsToken))
	t.Cleanup(ts.Close)
	t.Setenv("PROGRAMS_URL", ts.URL)
	t.Setenv("PROGRAMS_TOKEN", fakeProgramsToken)
	return fake
}

// set changes the fake's answers.
func (f *fakePrograms) set(change func(f *fakePrograms)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// changes is the enrollment changes core asked for.
func (f *fakePrograms) changes() ([]endedForTransfer, []uint) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]endedForTransfer{}, f.ended...), append([]uint{}, f.withdrawn...)
}

func (f *fakePrograms) EnrolledUserIDs(_ context.Context, classID uint) ([]uint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.enrolled[classID]), nil
}

func (f *fakePrograms) EligibleUserIDs(_ context.Context, classID uint) ([]uint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.eligible[classID]), nil
}

func (f *fakePrograms) TransferConflicts(context.Context, uint, uint) ([]programsapi.ResidentTransferProgramConflicts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.transferConflicts), nil
}

func (f *fakePrograms) EndEnrollmentsForTransfer(_ context.Context, userID, fromFacilityID uint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failChanges {
		return errors.New("programs is down")
	}
	f.ended = append(f.ended, endedForTransfer{userID, fromFacilityID})
	return nil
}

func (f *fakePrograms) WithdrawEnrollments(_ context.Context, userID uint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failChanges {
		return errors.New("programs is down")
	}
	f.withdrawn = append(f.withdrawn, userID)
	return nil
}

func (f *fakePrograms) ResidentPrograms(context.Context, uint) ([]programsapi.ResidentProgramClassInfo, error) {
	return []programsapi.ResidentProgramClassInfo{}, nil
}

func (f *fakePrograms) FacilityPrograms(_ context.Context, facilityIDs []uint) (map[uint]programsapi.FacilityPrograms, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	progs := map[uint]programsapi.FacilityPrograms{}
	for _, id := range facilityIDs {
		if p, ok := f.facilityPrograms[id]; ok {
			progs[id] = p
		}
	}
	return progs, nil
}

func (f *fakePrograms) ClassHistoryCreatedAt(_ context.Context, ids []uint) (map[uint]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	times := map[uint]time.Time{}
	for _, id := range ids {
		if at, ok := f.classHistoryAt[id]; ok {
			times[id] = at
		}
	}
	return times, nil
}

func (f *fakePrograms) EngagementOverview(context.Context, *time.Time, *time.Time, *uint) (programsapi.ProgramEngagementOverview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	overview := f.engagement
	overview.TopPrograms = orEmpty(overview.TopPrograms)
	return overview, nil
}

func (f *fakePrograms) SecondProgramEnrollment(context.Context, *uint) ([]programsapi.SecondProgramEnrollmentRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.secondEnrollment), nil
}

func (f *fakePrograms) CompletionMatrix(context.Context, *uint) ([]programsapi.ProgramCompletionMatrixCell, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.completionMatrix), nil
}

func (f *fakePrograms) LoadDistribution(context.Context, *uint) (programsapi.ProgramLoadDistribution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	load := f.loadDistribution
	load.Statewide = orEmpty(load.Statewide)
	load.ByFacility = orEmpty(load.ByFacility)
	return load, nil
}

func (f *fakePrograms) EnrollmentByProgramType(context.Context, *uint) ([]programsapi.ProgramTypeEnrollment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.enrollmentByType), nil
}

func (f *fakePrograms) ClassMetrics(context.Context, *uint) (programsapi.ClassDashboardMetrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.classMetrics, nil
}

func (f *fakePrograms) FacilityHealth(context.Context, *uint, int, string) ([]programsapi.FacilityHealthSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.facilityHealth), nil
}

func (f *fakePrograms) LiveUsers(context.Context, uint) ([]coreapi.ProviderUser, bool, error) {
	return nil, false, nil
}

func (f *fakePrograms) MappingsChanged(uint, uint) {}
