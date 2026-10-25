package models

type FacilityBlockingChildren struct {
	Users                int64 `json:"users"`
	Classes              int64 `json:"classes"`
	Programs             int64 `json:"programs"`
	Rooms                int64 `json:"rooms"`
	LoginActivity        int64 `json:"login_activity"`
	OpenContentActivity  int64 `json:"open_content_activity"`
	OpenContentFavorites int64 `json:"open_content_favorites"`
	VisibilityStatuses   int64 `json:"visibility_statuses"`
	UserHistory          int64 `json:"user_history"`
}

func (b FacilityBlockingChildren) HasAny() bool {
	return b.Users > 0 ||
		b.Classes > 0 ||
		b.Programs > 0 ||
		b.Rooms > 0 ||
		b.OpenContentActivity > 0 ||
		b.OpenContentFavorites > 0 ||
		b.UserHistory > 0
}
