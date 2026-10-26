package programs

import (
	"context"
	"fmt"
	"math"
)

type UsersService struct {
	db *Store
}

func NewUsersService(db *Store) *UsersService {
	return &UsersService{db: db}
}

func (svc *UsersService) GetWeeklyAttendanceTrend(ctx context.Context, userID, weeks int) ([]WeeklyAttendanceTrend, error) {
	rows, err := svc.db.GetUserWeeklyAttendanceRows(ctx, userID, weeks)
	if err != nil {
		return nil, err
	}
	trends := make([]WeeklyAttendanceTrend, 0, len(rows))
	for _, r := range rows {
		rate := 0.0
		if r.TotalCount > 0 {
			rate = math.Round(r.PresentCount / r.TotalCount * 100)
		}
		trends = append(trends, WeeklyAttendanceTrend{
			Week: fmt.Sprintf("%s %d", r.WeekStart.Format("Jan"), r.WeekStart.Day()),
			Rate: rate,
		})
	}
	return trends, nil
}
