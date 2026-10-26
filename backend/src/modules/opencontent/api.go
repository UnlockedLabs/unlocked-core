package opencontent

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"

	"gorm.io/gorm"
)

// openContentAPI answers core's questions (api.OpenContent). The queries
// moved from core's database package unchanged (EN-192).
type openContentAPI struct{ db *gorm.DB }

// NewAPI is Open Content's answers to core, reading db.
func NewAPI(db *gorm.DB) api.OpenContent {
	return openContentAPI{db: db}
}

func (a openContentAPI) EndActivity(_ context.Context, activityID int64) {
	(&store{db: a.db}).UpdateOpenContentActivityStopTS(activityID)
}

func (a openContentAPI) TopLibraries(ctx context.Context, userID, facilityID uint) ([]api.ContentUsage, error) {
	libraries := make([]api.ContentUsage, 0, 5)
	query := a.db.WithContext(ctx).Table("libraries lib ").
		Select(`lib.id as content_id,lib.title,
			lib.url,
			lib.thumbnail_url,
			CASE WHEN fvs.visibility_status IS NULL THEN false
				ELSE fvs.visibility_status
			END AS visibility_status,
			lib.open_content_provider_id,
			CASE WHEN ocf.facility_id IS NOT NULL AND ocf.facility_id = u.facility_id THEN true
				ELSE false
			END as is_featured,
			SUM(EXTRACT(EPOCH FROM oca.duration) / 3600) AS total_hours,
			SUM(EXTRACT(EPOCH FROM oca.duration) / 60) AS total_minutes
		`).
		Joins(`join open_content_providers ocp ON ocp.id = lib.open_content_provider_id
				AND ocp.currently_enabled = TRUE
				AND ocp.deleted_at IS NULL`).
		Joins(`join open_content_activities oca on oca.open_content_provider_id = ocp.id
				and oca.content_id = lib.id`).
		Joins(`join users u on u.id = oca.user_id
				and u.id = ?`, userID).
		Joins(`left outer join open_content_favorites ocf on ocf.open_content_provider_id = ocp.id
				and ocf.content_id = lib.id
				and ocf.facility_id IS NOT NULL`).
		Joins(`left outer join facility_visibility_statuses fvs on fvs.open_content_provider_id = lib.open_content_provider_id
			and fvs.content_id = lib.id
			and fvs.facility_id = ?`, facilityID).
		Where("oca.user_id = ?", userID).
		Group("lib.title, lib.url, lib.thumbnail_url, fvs.visibility_status, lib.open_content_provider_id, ocf.facility_id, u.facility_id, lib.id").
		Order("8 desc").
		Limit(5)
	if err := query.Find(&libraries).Error; err != nil {
		return nil, err
	}
	return libraries, nil
}

func (a openContentAPI) RecentVideos(ctx context.Context, userID uint) ([]api.ContentUsage, error) {
	videos := make([]api.ContentUsage, 0, 5)
	query := `with RecentVideos as (
		select oca.content_id
		from videos vid
		join open_content_providers ocp on ocp.id = vid.open_content_provider_id
			and ocp.currently_enabled = true
			and ocp.deleted_at IS NULL
		join open_content_activities oca on oca.content_id = vid.id
			and oca.open_content_provider_id = ocp.id
		where oca.user_id = ?
		and oca.request_ts = (
			select max(sub_oca.request_ts)
			from open_content_activities sub_oca
			where sub_oca.content_id = oca.content_id
			and sub_oca.user_id = oca.user_id
		)
		order by oca.request_ts desc
		limit 5
	),
	VideoWatchTime as (
		select oca.content_id,
		SUM(EXTRACT(EPOCH FROM oca.duration) / 60) AS total_minutes
		from open_content_activities oca
		join open_content_providers ocp on ocp.id = oca.open_content_provider_id
			and ocp.currently_enabled = true
			and ocp.deleted_at IS NULL
		join videos vid on vid.id = oca.content_id
			and vid.open_content_provider_id = ocp.id
		where oca.content_id in (select content_id from RecentVideos)
			and oca.user_id = ?
		group by oca.content_id
	)
	select vid.title, vid.url,
		vid.thumbnail_url,vid.description,
		vid.channel_title,
		vwt.total_minutes,
		vwt.total_minutes / 60 as total_hours
	from videos vid
	join VideoWatchTime vwt on vwt.content_id = vid.id
	order by vwt.total_minutes desc
	limit 5`

	if err := a.db.WithContext(ctx).Raw(query, userID, userID).Scan(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

func (a openContentAPI) ResourcesAccessed(ctx context.Context, userID uint) (int64, error) {
	var total int64
	if err := a.db.WithContext(ctx).
		Table("open_content_activities oca").
		Select("COUNT(DISTINCT (oca.open_content_provider_id, oca.content_id)) AS total_resources_accessed").
		Joins("inner join open_content_providers ocp ON ocp.id = oca.open_content_provider_id AND ocp.deleted_at IS NULL").
		Where("oca.user_id = ?", userID).
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (a openContentAPI) ActivityEngagement(ctx context.Context, userID uint) (api.ActivityEngagement, error) {
	var engagement api.ActivityEngagement
	err := a.db.WithContext(ctx).
		Table("open_content_activities oca").
		Select(`COUNT(DISTINCT CAST(oca.request_ts AS DATE)) AS total_active_days_monthly,
		AVG(EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 3600) AS total_hours_active_monthly,
		SUM(EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 3600) / 4 AS total_hours_active_weekly,
		SUM(EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 60) / 4 AS total_minutes_active_weekly,
		SUM(
			CASE
				WHEN oca.request_ts >= date_trunc('week', CURRENT_DATE)
				THEN EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 3600
				ELSE 0
			END
		) AS total_hours_engaged,
		SUM(
			CASE
				WHEN oca.request_ts >= date_trunc('week', CURRENT_DATE)
				THEN EXTRACT(EPOCH FROM (oca.stop_ts - oca.request_ts)) / 60
				ELSE 0
			END
		) AS total_minutes_engaged`).
		Where("oca.user_id = ?", userID).
		Scan(&engagement).Error
	return engagement, err
}

func (a openContentAPI) FacilityContent(ctx context.Context, facilityIDs []uint) (map[uint]api.FacilityContent, error) {
	content := make(map[uint]api.FacilityContent)
	if len(facilityIDs) == 0 {
		return content, nil
	}
	counts := []struct {
		table string
		set   func(*api.FacilityContent, int64)
	}{
		{"open_content_activities", func(c *api.FacilityContent, n int64) { c.Activities = n }},
		{"open_content_favorites", func(c *api.FacilityContent, n int64) { c.Favorites = n }},
		{"facility_visibility_statuses", func(c *api.FacilityContent, n int64) { c.VisibilityStatuses = n }},
	}
	for _, count := range counts {
		var rows []struct {
			FacilityID uint
			Count      int64
		}
		if err := a.db.WithContext(ctx).
			Table(count.table).
			Select("facility_id, COUNT(*) AS count").
			Where("facility_id IN ?", facilityIDs).
			Group("facility_id").
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			c := content[row.FacilityID]
			count.set(&c, row.Count)
			content[row.FacilityID] = c
		}
	}
	return content, nil
}

// Knowledge Center

func kcPriorWindow(start, end *time.Time) (*time.Time, *time.Time) {
	if start == nil || end == nil {
		return nil, nil
	}
	duration := end.Sub(*start)
	priorStart := start.Add(-duration)
	priorEnd := *start
	return &priorStart, &priorEnd
}

func kcPctChange(current, prior int64) int {
	if prior <= 0 {
		return 0
	}
	return int(math.Round(float64(current-prior) / float64(prior) * 100))
}

func (a openContentAPI) kcActivityScope(ctx context.Context, start, end *time.Time, facilityID *uint) *gorm.DB {
	tx := a.db.WithContext(ctx).Model(&OpenContentActivity{})
	if facilityID != nil {
		tx = tx.Where("facility_id = ?", *facilityID)
	}
	if start != nil && end != nil {
		tx = tx.Where("request_ts >= ? AND request_ts < ?", *start, *end)
	}
	return tx
}

func (a openContentAPI) Interactions(ctx context.Context, w api.Window) (api.Interactions, error) {
	var total int64
	if err := a.kcActivityScope(ctx, w.Start, w.End, w.FacilityID).Count(&total).Error; err != nil {
		return api.Interactions{}, err
	}
	var unique int64
	if err := a.kcActivityScope(ctx, w.Start, w.End, w.FacilityID).
		Distinct("user_id").Count(&unique).Error; err != nil {
		return api.Interactions{}, err
	}
	change := 0
	if priorStart, priorEnd := kcPriorWindow(w.Start, w.End); priorStart != nil {
		var priorTotal int64
		if err := a.kcActivityScope(ctx, priorStart, priorEnd, w.FacilityID).Count(&priorTotal).Error; err != nil {
			return api.Interactions{}, err
		}
		change = kcPctChange(total, priorTotal)
	}
	return api.Interactions{Total: total, Unique: unique, Change: change}, nil
}

func (a openContentAPI) AvgSessionMinutes(ctx context.Context, w api.Window) (float64, error) {
	type sessionRow struct {
		RequestTS time.Time
		StopTS    time.Time
	}
	rows := make([]sessionRow, 0)
	if err := a.kcActivityScope(ctx, w.Start, w.End, w.FacilityID).
		Select("request_ts, stop_ts").
		Where("stop_ts IS NOT NULL").
		Find(&rows).Error; err != nil {
		return 0, err
	}
	var totalMinutes float64
	var counted int
	for _, row := range rows {
		if row.StopTS.After(row.RequestTS) {
			totalMinutes += row.StopTS.Sub(row.RequestTS).Minutes()
			counted++
		}
	}
	if counted == 0 {
		return 0, nil
	}
	return totalMinutes / float64(counted), nil
}

func (a openContentAPI) RepeatEngagement(ctx context.Context, w api.Window) (api.RepeatEngagement, error) {
	type userCount struct {
		UserID uint
		Cnt    int64
	}
	counts := make([]userCount, 0)
	if err := a.kcActivityScope(ctx, w.Start, w.End, w.FacilityID).
		Select("user_id, count(*) as cnt").
		Group("user_id").
		Find(&counts).Error; err != nil {
		return api.RepeatEngagement{}, err
	}
	var engagement api.RepeatEngagement
	for _, row := range counts {
		switch {
		case row.Cnt >= 5:
			engagement.FivePlus++
		case row.Cnt >= 2:
			engagement.TwoToFour++
		default:
			engagement.Once++
		}
	}
	return engagement, nil
}

func (a openContentAPI) LibraryViewsByCategory(ctx context.Context, w api.Window) ([]api.CategoryViews, error) {
	views := make([]api.CategoryViews, 0)
	tx := a.db.WithContext(ctx).Table("open_content_activities oca").
		Select("t.name as category, count(oca.id) as views").
		Joins("JOIN open_content_tags oct ON oct.content_id = oca.content_id AND oct.open_content_provider_id = oca.open_content_provider_id").
		Joins("JOIN tags t ON t.id = oct.tag_id")
	if w.FacilityID != nil {
		tx = tx.Where("oca.facility_id = ?", *w.FacilityID)
	}
	if w.Start != nil && w.End != nil {
		tx = tx.Where("oca.request_ts >= ? AND oca.request_ts < ?", *w.Start, *w.End)
	}
	if err := tx.Group("t.name").Order("views DESC, t.name ASC").Scan(&views).Error; err != nil {
		return nil, err
	}
	return views, nil
}

func (a openContentAPI) TopLibraryVisits(ctx context.Context, w api.Window, limit int) ([]api.ContentVisits, error) {
	return a.topContentVisits(ctx, "libraries", "l", w, limit)
}

func (a openContentAPI) TopVideoVisits(ctx context.Context, w api.Window, limit int) ([]api.ContentVisits, error) {
	return a.topContentVisits(ctx, "videos", "v", w, limit)
}

func (a openContentAPI) topContentVisits(ctx context.Context, table, alias string, w api.Window, limit int) ([]api.ContentVisits, error) {
	type contentRow struct {
		ContentID uint
		Title     string
		Visits    int64
	}
	current := make([]contentRow, 0, limit)
	tx := a.db.WithContext(ctx).Table("open_content_activities oca").
		Select(alias + ".id as content_id, " + alias + ".title as title, count(oca.id) as visits").
		Joins("JOIN " + table + " " + alias + " ON " + alias + ".id = oca.content_id AND " + alias + ".open_content_provider_id = oca.open_content_provider_id AND " + alias + ".deleted_at IS NULL")
	if w.FacilityID != nil {
		tx = tx.Where("oca.facility_id = ?", *w.FacilityID)
	}
	if w.Start != nil && w.End != nil {
		tx = tx.Where("oca.request_ts >= ? AND oca.request_ts < ?", *w.Start, *w.End)
	}
	if err := tx.Group(alias + ".id, " + alias + ".title").
		Order("visits DESC, " + alias + ".title ASC").
		Limit(limit).
		Scan(&current).Error; err != nil {
		return nil, err
	}
	rows := make([]api.ContentVisits, 0, len(current))
	if len(current) == 0 {
		return rows, nil
	}

	priorVisits := make(map[uint]int64)
	if priorStart, priorEnd := kcPriorWindow(w.Start, w.End); priorStart != nil {
		ids := make([]uint, 0, len(current))
		for _, c := range current {
			ids = append(ids, c.ContentID)
		}
		priors := make([]contentRow, 0, len(ids))
		ptx := a.db.WithContext(ctx).Table("open_content_activities oca").
			Select(alias+".id as content_id, count(oca.id) as visits").
			Joins("JOIN "+table+" "+alias+" ON "+alias+".id = oca.content_id AND "+alias+".open_content_provider_id = oca.open_content_provider_id AND "+alias+".deleted_at IS NULL").
			Where(alias+".id IN ?", ids).
			Where("oca.request_ts >= ? AND oca.request_ts < ?", *priorStart, *priorEnd)
		if w.FacilityID != nil {
			ptx = ptx.Where("oca.facility_id = ?", *w.FacilityID)
		}
		if err := ptx.Group(alias + ".id").Scan(&priors).Error; err != nil {
			return nil, err
		}
		for _, p := range priors {
			priorVisits[p.ContentID] = p.Visits
		}
	}

	for _, c := range current {
		rows = append(rows, api.ContentVisits{
			Title:  c.Title,
			Visits: c.Visits,
			Change: kcPctChange(c.Visits, priorVisits[c.ContentID]),
		})
	}
	return rows, nil
}

// Demo seed

func (a openContentAPI) ResetDemoActivity(ctx context.Context, before time.Time) error {
	return a.db.WithContext(ctx).Exec("DELETE from open_content_activities where date(request_ts) <= date(?)", before).Error
}

func (a openContentAPI) SeedDemoActivity(ctx context.Context, facilityID uint, userIDs []uint) error {
	db := a.db.WithContext(ctx)
	libraries := []Library{}
	if err := db.Model(&Library{}).Limit(40).Find(&libraries).Error; err != nil {
		return err
	}
	contentUrls := []OpenContentUrl{}
	if err := db.Model(&OpenContentUrl{}).Find(&contentUrls).Error; err != nil {
		return err
	}
	if len(contentUrls) == 0 {
		newContentUrls := []OpenContentUrl{
			{ContentURL: "/"}, {ContentURL: "/math"}, {ContentURL: "/science"}, {ContentURL: "/history"},
		}
		if err := db.CreateInBatches(&newContentUrls, 4).Error; err != nil {
			return err
		}
		contentUrls = newContentUrls
	}
	sixMonthsAgo := time.Now().AddDate(0, -6, 0)
	for _, userID := range userIDs {
		for i := 0; i < 180; i++ {
			for _, library := range libraries {
				if i%(rand.Intn(8)+1) == 0 {
					openContentActivity := OpenContentActivity{
						UserID:                userID,
						RequestTS:             sixMonthsAgo.AddDate(0, 0, i).Add(time.Duration(i) * time.Minute),
						ContentID:             library.ID,
						OpenContentProviderID: library.OpenContentProviderID,
						OpenContentUrlID:      uint(rand.Intn(len(contentUrls)) + 1),
						FacilityID:            facilityID,
					}
					if err := db.Create(&openContentActivity).Error; err != nil {
						continue
					}
				}
			}
		}
	}
	return nil
}
