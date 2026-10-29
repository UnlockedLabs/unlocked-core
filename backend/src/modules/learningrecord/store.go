package learningrecord

import (
	"errors"
	"net/http"

	"github.com/UnlockedLabs/unlocked-contracts/identity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store is Learning Record's database. Its queries moved from core's
// database package unchanged (EN-195).
type Store struct{ *gorm.DB }

// WithUserContext is the store with r's context, so the audit hooks and the
// explicit actor reads in DeleteEntry and UpsertDraft record the caller as
// the row's update user, the same as core's srv.WithUserContext(r) did.
func (srv *server) WithUserContext(r *http.Request) *Store {
	return &Store{srv.store.WithContext(r.Context())}
}

func withFacilityName(db *Store) *gorm.DB {
	return db.Model(&LearningRecordEntry{}).
		Select("learning_record_entries.*, COALESCE(facilities.name, '') AS facility_name").
		Joins("LEFT JOIN facilities ON facilities.id = learning_record_entries.facility_id")
}

/*
GetLocationFacility resolves a facility id used as an achievement location,
returning the facility's name and whether it exists at all. The lookup is
unscoped on purpose: an achievement can point at a facility that has since
been soft-deleted, and the foreign key still holds, so rejecting those would
block edits to older records. ok is false only for an id that is not a
facility.
*/
func (db *Store) GetLocationFacility(id uint) (string, bool, error) {
	var names []string
	if err := db.Unscoped().Model(&facility{}).
		Where("id = ?", id).
		Limit(1).
		Pluck("name", &names).Error; err != nil {
		return "", false, newGetRecordsDBError(err, "facilities")
	}
	if len(names) == 0 {
		return "", false, nil
	}
	return names[0], true, nil
}

// GetAllFacilitiesOrdered is every facility Learning Record's location picker
// offers, alphabetical. GORM's default scope excludes a soft-deleted one.
func (db *Store) GetAllFacilitiesOrdered() ([]facility, error) {
	var facilities []facility
	if err := db.Order("name").Find(&facilities).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}
	return facilities, nil
}

func (db *Store) GetEntries(userID uint) ([]LearningRecordEntry, error) {
	entries := make([]LearningRecordEntry, 0)
	if err := withFacilityName(db).
		Where("learning_record_entries.user_id = ? AND learning_record_entries.is_draft = false", userID).
		Order("learning_record_entries.created_at DESC").
		Find(&entries).Error; err != nil {
		return nil, newGetRecordsDBError(err, "learning_record_entries")
	}
	return entries, nil
}

func (db *Store) CreateEntry(entry *LearningRecordEntry) error {
	if err := db.Create(entry).Error; err != nil {
		return newCreateDBError(err, "learning_record_entries")
	}
	return nil
}

func (db *Store) UpdateEntry(entry *LearningRecordEntry) error {
	result := db.Model(entry).
		Where("id = ? AND user_id = ?", entry.ID, entry.UserID).
		Updates(entryColumns(entry))
	if result.Error != nil {
		return newUpdateDBError(result.Error, "learning_record_entries")
	}
	return nil
}

func (db *Store) DeleteEntry(id, userID uint) error {
	actorID := userID
	if ctx := db.Statement.Context; ctx != nil {
		if c, ok := identity.FromContext(ctx); ok {
			actorID = c.UserID
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&LearningRecordEntry{}).
			Where("id = ? AND user_id = ?", id, userID).
			Update("update_user_id", actorID).Error; err != nil {
			return newUpdateDBError(err, "learning_record_entries")
		}
		if err := tx.Where("id = ? AND user_id = ?", id, userID).
			Delete(&LearningRecordEntry{}).Error; err != nil {
			return newDeleteDBError(err, "learning_record_entries")
		}
		history := NewUserAccountHistory(userID, LearningRecordDeleted, &actorID)
		if err := tx.Create(history).Error; err != nil {
			return newCreateDBError(err, "user_account_history")
		}
		return nil
	})
}

// GetDraft returns the most recently updated draft for the user, or nil.
func (db *Store) GetDraft(userID uint) (*LearningRecordEntry, error) {
	var draft LearningRecordEntry
	err := withFacilityName(db).
		Where("learning_record_entries.user_id = ? AND learning_record_entries.is_draft = true", userID).
		Order("learning_record_entries.updated_at DESC").
		First(&draft).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, newGetRecordsDBError(err, "learning_record_entries")
	}
	return &draft, nil
}

// UpsertDraft inserts or updates a draft row keyed on (user_id, client_id).
// Its ON CONFLICT target doesn't match migration 00069's partial unique
// index, so it 500s on every call, in production too -- see README.md's
// Tests section.
func (db *Store) UpsertDraft(draft *LearningRecordEntry) error {
	draft.IsDraft = true
	updateMap := draftColumns(draft)
	updateMap["is_draft"] = true
	updateMap["updated_at"] = gorm.Expr("NOW()")
	if ctx := db.Statement.Context; ctx != nil {
		if c, ok := identity.FromContext(ctx); ok {
			updateMap["update_user_id"] = c.UserID
		}
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "client_id"}},
		DoUpdates: clause.Assignments(updateMap),
	}).Create(draft).Error; err != nil {
		return newCreateDBError(err, "learning_record_entries")
	}
	return nil
}

func (db *Store) DeleteDraft(userID uint, clientID string) error {
	if err := db.Where("user_id = ? AND client_id = ? AND is_draft = true", userID, clientID).
		Delete(&LearningRecordEntry{}).Error; err != nil {
		return newDeleteDBError(err, "learning_record_entries")
	}
	return nil
}

// entryColumns holds the achievement fields shared by drafts and committed
// entries. The draft-only wizard state (step_index, ui_phase,
// editing_entry_id) is excluded so editing a committed entry cannot reset it.
func entryColumns(e *LearningRecordEntry) map[string]any {
	return map[string]any{
		"program_name": e.ProgramName,
		"facility_id":  e.FacilityID, "facility_other": e.FacilityOther,
		"completion_date": e.CompletionDate, "confidence": e.Confidence,
		"summary": e.Summary, "top_skills": e.TopSkills,
		"barrier_to_completion": e.BarrierToCompletion, "goal_connection": e.GoalConnection,
		"pride": e.Pride, "standout_moment": e.StandoutMoment,
		"advice_to_peer": e.AdviceToPeer, "challenge_toggle": e.ChallengeToggle,
		"challenge_text": e.ChallengeText, "skill_tags_before": e.SkillTagsBefore,
		"skill_tags_after": e.SkillTagsAfter, "skill_reflection": e.SkillReflection,
		"growth_reflection": e.GrowthReflection, "support_selections": e.SupportSelections,
		"next_step_selections": e.NextStepSelections,
	}
}

// draftColumns adds the draft-only wizard state to the shared achievement
// fields; use it for draft upserts only.
func draftColumns(e *LearningRecordEntry) map[string]any {
	columns := entryColumns(e)
	columns["step_index"] = e.StepIndex
	columns["ui_phase"] = e.UiPhase
	columns["editing_entry_id"] = e.EditingEntryID
	return columns
}
