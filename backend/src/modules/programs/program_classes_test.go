package programs

import (
	"strings"
	"testing"
)

// CompletedOutcomeStatuses is derived from IsTerminalEnrollment at package init,
// but AllEnrollmentStatuses it reads from is hand-maintained. Adding a status to
// the ProgramEnrollmentStatus const block without listing it there would silently
// drop that status from every completion-rate denominator, so pin both ends.
func TestCompletedOutcomeStatuses(t *testing.T) {
	for _, s := range AllEnrollmentStatuses {
		if !IsTerminalEnrollment(s) {
			continue
		}
		found := false
		for _, got := range CompletedOutcomeStatuses {
			if got == s {
				found = true
				break
			}
		}
		// Cancelled is terminal but never had a completion opportunity.
		want := s != EnrollmentCancelled
		if found != want {
			t.Errorf("CompletedOutcomeStatuses contains %q = %v, want %v", s, found, want)
		}
	}

	for _, s := range CompletedOutcomeStatuses {
		if s == Enrolled || s == EnrollmentCancelled {
			t.Errorf("CompletedOutcomeStatuses must not contain %q", s)
		}
	}
}

// Every "Incomplete:" constant must be listed in AllEnrollmentStatuses, otherwise
// the derived sets above silently omit it.
func TestAllEnrollmentStatusesCoversIncompletePrefix(t *testing.T) {
	declared := []ProgramEnrollmentStatus{
		EnrollmentIncompleteWithdrawn,
		EnrollmentIncompleteDropped,
		EnrollmentIncompleteFailedToComplete,
		EnrollmentIncompleteTransfered,
		EnrollmentIncompleteSegregated,
	}
	for _, s := range declared {
		if !strings.HasPrefix(string(s), "Incomplete:") {
			t.Fatalf("%q is not an Incomplete: status; fix this test's list", s)
		}
		found := false
		for _, got := range AllEnrollmentStatuses {
			if got == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AllEnrollmentStatuses is missing %q", s)
		}
	}
}
