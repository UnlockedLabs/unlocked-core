package programs

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Store is Programs' database. Its queries moved from core's database package
// unchanged (EN-194). It is exported for the integration tests, which call
// the queries directly; depguard keeps core from using it.
type Store struct{ *gorm.DB }

// WithUserContext is the store with r's context, so the audit hooks record
// the caller as the row's create or update user.
func (srv *server) WithUserContext(r *http.Request) *Store {
	return &Store{srv.store.WithContext(r.Context())}
}

// Validate is the validator for Programs' models and requests.
var Validate = sync.OnceValue(func() *validator.Validate {
	return validator.New(validator.WithRequiredStructEnabled())
})

// The reads of core's users and facilities tables that Programs makes, copied
// from core's database package.

func (db *Store) GetUserByID(id uint) (*User, error) {
	user := User{}
	if err := db.First(&user, id).Error; err != nil {
		return nil, newNotFoundDBError(err, "users")
	}
	return &user, nil
}

func (db *Store) DeactivatedUsersPresent(userIDs []int) (bool, error) {
	if len(userIDs) == 0 {
		return false, nil
	}
	var count int64
	if err := db.Model(&User{}).Where("id IN (?) AND deactivated_at IS NOT NULL", userIDs).Count(&count).Error; err != nil {
		log.Errorf("Error checking deactivated users: %v", err)
		return false, newGetRecordsDBError(err, "users")
	}
	return count > 0, nil
}

func (db *Store) GetAllFacilitiesOrdered() ([]Facility, error) {
	var facilities []Facility
	if err := db.Order("name").Find(&facilities).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}
	return facilities, nil
}

func (db *Store) GetFacilityByID(id int) (*Facility, error) {
	var facility Facility
	if err := db.Where("id = ?", fmt.Sprintf("%d", id)).First(&facility).Error; err != nil {
		return nil, newNotFoundDBError(err, "facilities")
	}
	return &facility, nil
}

func (db *Store) GetFacilityNamesByIDs(ids []uint) ([]string, error) {
	var names []string
	if len(ids) == 0 {
		return names, nil
	}
	if err := db.Model(&Facility{}).Where("id IN ?", ids).Order("name").Pluck("name", &names).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}
	return names, nil
}

func adjustUserOrderBy(arg string) string {
	if strings.Contains(arg, "name_last") {
		return arg + ", name_first"
	}
	return arg
}

func StringPtr(s string) *string {
	return &s
}

// NilIfBlank returns nil for empty or whitespace-only strings so they are stored as NULL.
func NilIfBlank(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func UintPtr(u uint) *uint {
	return &u
}

func DerefUint(ptr *uint) uint {
	if ptr == nil {
		return 0
	}
	return *ptr
}
