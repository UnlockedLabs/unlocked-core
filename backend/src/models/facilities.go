package models

import "time"

type FacilityWithStats struct {
	ID             uint      `json:"id"`
	Name           string    `json:"name"`
	Timezone       string    `json:"timezone"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ActivePrograms int64     `json:"active_programs"`
	ActiveClasses  int64     `json:"active_classes"`
	TotalResidents int64     `json:"total_residents"`
	// CanDelete mirrors FacilityBlockingChildren.HasAny(): true when the
	// facility has no association that blocks deletion. Drives the delete
	// control's enabled state in the UI.
	CanDelete bool `json:"can_delete"`
}

type Facility struct {
	DatabaseFields
	Name     string `gorm:"size:255;not null" json:"name"`
	Timezone string `gorm:"size:255;not null" json:"timezone" validate:"required,timezone"`

	Users []User `gorm:"foreignKey:FacilityID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;" json:"-"`
	// Programs is always null. It keeps the JSON a facility had before
	// Programs moved into its module (EN-194).
	Programs      []struct{}      `json:"programs" gorm:"-"`
	LoginActivity []LoginActivity `json:"login_activity" gorm:"foreignKey:FacilityID;constraint:OnDelete CASCADE"`
}

func (Facility) TableName() string {
	return "facilities"
}
