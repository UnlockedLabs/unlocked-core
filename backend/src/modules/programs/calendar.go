package programs

import "time"

const (
	Onwards string = "onwards"
	Self    string = "self"
	All     string = "all"
)

type EventInstance struct {
	EventID     uint          `json:"event_id"`
	CohortID    uint          `json:"cohort_id"`
	ProgramName string        `json:"program_name"`
	StartTime   time.Time     `json:"start_time"`
	Duration    time.Duration `json:"duration"`
	Room        string        `json:"location"`
	IsCancelled bool          `json:"is_cancelled"`
}

type Day struct {
	DayIdx int             `json:"day_index"`
	Date   time.Time       `json:"date"`
	Events []EventInstance `json:"events"`
}

type OverrideForm struct {
	RoomID       *uint  `json:"room_id"`
	Date         string `json:"date"`
	StartTime    string `json:"start_time"`
	Duration     string `json:"duration"`
	IsCancelled  bool   `json:"is_cancelled"`
	OverrideType string `json:"override_type"`
}

const (
	OverrideAll      string = "all"
	OverrideForwards string = "forwards"
	OverrideSelf     string = "self"
)
