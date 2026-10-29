package models

import (
	"crypto/rand"
	"math/big"
	"slices"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/users"
	"gorm.io/gorm"
)

type UserRole = users.Role

type contextKey string

const (
	UserIDKey       contextKey = "user_id"
	SystemAdmin                = users.SystemAdmin
	FacilityAdmin              = users.FacilityAdmin
	DepartmentAdmin            = users.DepartmentAdmin
	Student                    = users.Student
)

var AdminRoles = users.AdminRoles

type DatabaseFields struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// Audit fields - track which user created/updated the record
	CreateUserID *uint `json:"create_user_id"`
	UpdateUserID *uint `json:"update_user_id"`
}

func (m *DatabaseFields) BeforeCreate(tx *gorm.DB) error {
	if userID, ok := tx.Statement.Context.Value(UserIDKey).(uint); ok {
		m.CreateUserID = &userID
		return nil
	}
	return nil
}

func (m *DatabaseFields) BeforeUpdate(tx *gorm.DB) error {
	if userID, ok := tx.Statement.Context.Value(UserIDKey).(uint); ok {
		m.UpdateUserID = &userID
		tx.Statement.SetColumn("update_user_id", userID)
	}
	return nil
}

type Role struct {
	Name UserRole `json:"name" gorm:"primaryKey"`
}

type User struct {
	DatabaseFields
	Username      string     `gorm:"size:255;not null;unique" json:"username" validate:"alphanumperiod"`
	NameFirst     string     `gorm:"size:255;not null" json:"name_first"  validate:"alphanumspace"`
	Email         string     `gorm:"size:255;not null;unique" json:"email" validate:"-"`
	NameLast      string     `gorm:"size:255;not null" json:"name_last"  validate:"alphanumspace"`
	Role          UserRole   `gorm:"size:64;default:student" json:"role" validate:"oneof=student system_admin facility_admin department_admin"`
	KratosID      *string    `gorm:"size:255" json:"kratos_id"`
	FacilityID    uint       `json:"facility_id"`
	DocID         *string    `json:"doc_id" gorm:"column:doc_id;size:32"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`

	/* foreign keys */
	Facility     *Facility     `json:"facility,omitempty" gorm:"foreignKey:FacilityID;constraint:OnDelete SET NULL"`
	UserRole     *Role         `json:"-" gorm:"foreignKey:Role;constraint:OnDelete SET NULL"`
	LoginMetrics *LoginMetrics `json:"login_metrics,omitempty" gorm:"foreignKey:UserID;constraint:OnDelete CASCADE"`
}

func (User) TableName() string {
	return "users"
}

func (usr *User) BeforeCreate(tx *gorm.DB) error {
	if err := usr.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}
	if usr.Email == "" {
		usr.Email = usr.Username + "@unlocked.v2"
	}
	return nil
}

const (
	passwordLength       = 8
	unambiguousLowercase = "abcdefghijkmnpqrstuvwxyz" // Removes: l (looks like 1), o (looks like 0)
)

func (user *User) CreateTempPassword() (string, error) {
	charset := unambiguousLowercase
	password := make([]byte, passwordLength)

	for i := range password {
		randomIndex, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		password[i] = charset[randomIndex.Int64()]
	}

	return string(password), nil
}

func (user *User) GetTraits() map[string]any {
	return map[string]any{
		"username":    user.Username,
		"role":        user.Role,
		"facility_id": user.FacilityID,
	}
}

func (user *User) IsAdmin() bool {
	return slices.Contains(AdminRoles, user.Role)
}

type UserAccountHistory struct {
	UserID                  uint                  `json:"user_id" gorm:"primaryKey"`
	AdminID                 *uint                 `json:"admin_id"`
	Action                  ActivityHistoryAction `json:"action" gorm:"size:255;primaryKey"`
	ProgramClassesHistoryID *uint                 `json:"program_classes_history_id"`
	FacilityID              *uint                 `json:"facility_id"`
	CreatedAt               time.Time             `json:"created_at" gorm:"primaryKey"`
	AttendanceStatus        string                `json:"attendance_status" gorm:"size:27"`
	ClassName               *string               `json:"class_name" gorm:"size:255"`
	SessionDate             *time.Time            `json:"session_date" gorm:"type:date"`

	User     *User     `json:"user,omitempty" gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Admin    *User     `json:"admin,omitempty" gorm:"foreignKey:AdminID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Facility *Facility `json:"facility,omitempty" gorm:"foreignKey:FacilityID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

func (UserAccountHistory) TableName() string {
	return "user_account_history"
}

type ActivityHistoryAction string

const (
	AccountCreation    ActivityHistoryAction = "account_creation"
	FacilityTransfer   ActivityHistoryAction = "facility_transfer"
	SetPassword        ActivityHistoryAction = "set_password"
	ResetPassword      ActivityHistoryAction = "reset_password"
	ProgClassHistory   ActivityHistoryAction = "progclass_history"
	UserDeactivated    ActivityHistoryAction = "user_deactivated"
	AttendanceRecorded ActivityHistoryAction = "attendance_recorded"
)

type ActivityHistoryResponse struct {
	Action                  ActivityHistoryAction `json:"action"`
	CreatedAt               *time.Time            `json:"created_at"`
	FieldName               *string               `json:"field_name"`
	NewValue                *string               `json:"new_value"`
	OldValue                *string               `json:"old_value"`
	UserID                  *uint                 `json:"user_id"`
	UserUsername            *string               `json:"user_username"`
	AdminUsername           *string               `json:"admin_username"`
	FacilityName            *string               `json:"facility_name"`
	ProgramClassesHistoryID *uint                 `json:"program_classes_history_id"`
	AttendanceStatus        *string               `json:"attendance_status"`
	ClassName               *string               `json:"class_name"`
	SessionDate             *time.Time            `json:"session_date"`
	// ClassHistoryID is the program_classes_history row the action logged,
	// whose time Programs gives the handler. The response has never sent it.
	ClassHistoryID *uint `json:"-"`
}

func NewUserAccountHistory(userID uint, action ActivityHistoryAction, adminID *uint, programClassesHistoryID *uint, facilityID *uint) *UserAccountHistory {
	return &UserAccountHistory{
		UserID:                  userID,
		AdminID:                 adminID,
		Action:                  action,
		ProgramClassesHistoryID: programClassesHistoryID,
		FacilityID:              facilityID,
		CreatedAt:               time.Now(),
	}
}

type UserNote struct {
	DatabaseFields
	UserID uint   `json:"user_id" gorm:"not null"`
	Note   string `json:"note" gorm:"not null"`

	Creator *User `json:"creator,omitempty" gorm:"foreignKey:CreateUserID;references:ID"`
}

func (UserNote) TableName() string { return "user_notes" }

type UserNoteResponse struct {
	ID    uint      `json:"id"`
	Date  time.Time `json:"date"`
	Admin string    `json:"admin"`
	Note  string    `json:"note"`
}

type ValidatedUserRow struct {
	RowNumber  int    `json:"row_number"`
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name"`
	ResidentID string `json:"resident_id"`
	Username   string `json:"username"`
}

type InvalidUserRow struct {
	ValidatedUserRow
	ErrorReasons []string `json:"error_reasons"`
}

type BulkUploadResponse struct {
	ValidCount   int                `json:"valid_count"`
	ErrorCount   int                `json:"error_count"`
	ValidRows    []ValidatedUserRow `json:"valid_rows"`
	InvalidRows  []InvalidUserRow   `json:"invalid_rows"`
	ErrorCSVData []byte             `json:"error_csv_data,omitempty"`
}
