package programs

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

type ClassesService struct {
	db *Store
}

func NewClassesService(db *Store) *ClassesService {
	return &ClassesService{db: db}
}

func (svc *ClassesService) GetFacilityHealthSummaries(args *QueryContext, facilityID *uint, days int) ([]FacilityHealthSummary, error) {
	summaries, err := svc.db.GetFacilityHealthSummaries(args, facilityID)
	if err != nil {
		return nil, err
	}

	attendanceConcerns, err := svc.db.GetFacilityAttendanceConcerns(args, facilityID)
	if err != nil {
		return nil, err
	}

	items, classByID, err := svc.getMissingAttendanceForFacilityData(args, facilityID, days)
	if err != nil {
		return nil, err
	}

	missingByFacilityClasses := make(map[uint]map[uint]struct{})
	for _, item := range items {
		classInfo, ok := classByID[item.CohortID]
		if !ok {
			continue
		}
		if _, exists := missingByFacilityClasses[classInfo.FacilityID]; !exists {
			missingByFacilityClasses[classInfo.FacilityID] = make(map[uint]struct{})
		}
		missingByFacilityClasses[classInfo.FacilityID][item.CohortID] = struct{}{}
	}
	missingByFacility := make(map[uint]int64, len(missingByFacilityClasses))
	for facilityRef, classIDs := range missingByFacilityClasses {
		missingByFacility[facilityRef] = int64(len(classIDs))
	}

	for i := range summaries {
		facilityRef := summaries[i].FacilityID
		summaries[i].AttendanceConcerns = attendanceConcerns[facilityRef]
		summaries[i].MissingAttendance = missingByFacility[facilityRef]
	}

	return summaries, nil
}

func (svc *ClassesService) GetMissingAttendanceForFacility(args *QueryContext, facilityID *uint, days int) ([]MissingAttendanceItem, error) {
	items, _, err := svc.getMissingAttendanceForFacilityData(args, facilityID, days)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (svc *ClassesService) getMissingAttendanceForFacilityData(args *QueryContext, facilityID *uint, days int) ([]MissingAttendanceItem, map[uint]MissingAttendanceClass, error) {
	if days <= 0 {
		days = 3
	}
	classes, err := svc.db.GetActiveClassesForMissingAttendance(args, facilityID)
	if err != nil {
		return nil, nil, err
	}
	if len(classes) == 0 {
		return []MissingAttendanceItem{}, map[uint]MissingAttendanceClass{}, nil
	}

	loc, err := time.LoadLocation(args.Timezone)
	if err != nil {
		loc = time.UTC
	}
	nowLocal := time.Now().In(loc)
	startDate := time.Now().In(loc).AddDate(0, 0, -days).Truncate(24 * time.Hour)
	endDate := time.Now().In(loc).AddDate(0, 0, 1).Truncate(24 * time.Hour)

	classIDs := make([]uint, 0, len(classes))
	classByID := make(map[uint]MissingAttendanceClass, len(classes))
	for _, cls := range classes {
		classIDs = append(classIDs, cls.ID)
		classByID[cls.ID] = cls
	}

	events, err := svc.db.GetClassEventsWithOverrides(args, classIDs)
	if err != nil {
		return nil, nil, err
	}

	type eventInstance struct {
		EventInstance
		DateKey string
	}
	instances := make([]eventInstance, 0)
	eventIDSet := make(map[uint]struct{})
	dateSet := make(map[string]struct{})

	for _, event := range events {
		rule, err := event.GetRRuleWithTimezone(args.Timezone)
		if err != nil {
			continue
		}
		firstOccurrence := rule.After(time.Time{}, false)
		canonicalHour, canonicalMinute := getCanonicalHourAndMinute([]time.Time{firstOccurrence}, args.Timezone)
		eventInstances := GenerateEventInstances(event, startDate, endDate)
		for _, inst := range eventInstances {
			if inst.IsCancelled {
				continue
			}
			localStart := inst.StartTime.In(loc)
			consistentOccurrence := time.Date(
				localStart.Year(),
				localStart.Month(),
				localStart.Day(),
				canonicalHour,
				canonicalMinute,
				0,
				0,
				loc,
			).UTC()
			if consistentOccurrence.After(nowLocal) {
				continue
			}
			inst.StartTime = consistentOccurrence
			dateKey := consistentOccurrence.In(loc).Format("2006-01-02")
			instances = append(instances, eventInstance{
				EventInstance: inst,
				DateKey:       dateKey,
			})
			eventIDSet[inst.EventID] = struct{}{}
			dateSet[dateKey] = struct{}{}
		}
	}
	if len(instances) == 0 {
		return []MissingAttendanceItem{}, classByID, nil
	}

	eventIDs := make([]uint, 0, len(eventIDSet))
	for id := range eventIDSet {
		eventIDs = append(eventIDs, id)
	}
	dates := make([]string, 0, len(dateSet))
	for date := range dateSet {
		dates = append(dates, date)
	}

	attendanceCounts, err := svc.db.GetAttendanceCountsForEvents(args, eventIDs, dates)
	if err != nil {
		return nil, nil, err
	}
	attendanceMap := make(map[string]int64, len(attendanceCounts))
	for _, attendance := range attendanceCounts {
		key := fmt.Sprintf("%d_%s", attendance.EventID, attendance.Date)
		attendanceMap[key] = attendance.Count
	}

	type enrollmentWindow struct {
		EnrolledAt        *time.Time
		EnrollmentEndedAt *time.Time
	}
	enrollments, err := svc.db.GetActiveEnrollmentsForClasses(args, classIDs)
	if err != nil {
		return nil, nil, err
	}
	enrollmentsByCohort := make(map[uint][]enrollmentWindow, len(classIDs))
	for _, enrollment := range enrollments {
		enrollmentsByCohort[enrollment.CohortID] = append(enrollmentsByCohort[enrollment.CohortID], enrollmentWindow{
			EnrolledAt:        enrollment.EnrolledAt,
			EnrollmentEndedAt: enrollment.EnrollmentEndedAt,
		})
	}

	items := make([]MissingAttendanceItem, 0)
	for _, inst := range instances {
		enrolledCountOnDate := 0
		for _, enrollment := range enrollmentsByCohort[inst.CohortID] {
			if enrollment.EnrolledAt == nil {
				continue
			}
			if !enrollment.EnrolledAt.After(inst.StartTime) &&
				(enrollment.EnrollmentEndedAt == nil || enrollment.EnrollmentEndedAt.After(inst.StartTime.AddDate(0, 0, -1))) {
				enrolledCountOnDate++
			}
		}
		if enrolledCountOnDate == 0 {
			continue
		}
		key := fmt.Sprintf("%d_%s", inst.EventID, inst.DateKey)
		attendanceCount := attendanceMap[key]
		if int(attendanceCount) == enrolledCountOnDate {
			continue
		}
		classInfo := classByID[inst.CohortID]
		items = append(items, MissingAttendanceItem{
			CohortID:     inst.CohortID,
			ClassName:    classInfo.Name,
			FacilityName: classInfo.FacilityName,
			EventID:      inst.EventID,
			Date:         inst.DateKey,
			StartTime:    inst.StartTime.In(loc).Format("15:04"),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Date == items[j].Date {
			return items[i].StartTime > items[j].StartTime
		}
		return items[i].Date > items[j].Date
	})

	return items, classByID, nil
}

func (svc *ClassesService) GetTodaysSchedule(args *QueryContext, facilityID *uint) ([]TodaysScheduleItem, error) {
	classes, err := svc.db.GetActiveClassesWithEvents(args, facilityID)
	if err != nil {
		return nil, err
	}
	if len(classes) == 0 {
		return []TodaysScheduleItem{}, nil
	}

	loc, err := time.LoadLocation(args.Timezone)
	if err != nil {
		loc = time.UTC
	}
	nowLocal := time.Now().In(loc)
	startOfToday := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	endOfToday := startOfToday.AddDate(0, 0, 1)

	items := make([]TodaysScheduleItem, 0)
	for _, class := range classes {
		startDt := class.StartDt.In(loc)
		if startDt.After(endOfToday) {
			continue
		}
		if class.EndDt != nil {
			endDt := class.EndDt.In(loc)
			if endDt.Before(startOfToday) {
				continue
			}
		}

		for _, event := range class.Events {
			rule, err := event.GetRRuleWithTimezone(args.Timezone)
			if err != nil {
				continue
			}
			firstOccurrence := rule.After(time.Time{}, false)
			canonicalHour, canonicalMinute := getCanonicalHourAndMinute([]time.Time{firstOccurrence}, args.Timezone)
			overrideStartTimes := make(map[string]struct{})
			for _, override := range event.Overrides {
				if override.IsCancelled {
					continue
				}
				overrideOptions, err := rrule.StrToROption(override.OverrideRrule)
				if err != nil {
					continue
				}
				overrideOptions.Dtstart = overrideOptions.Dtstart.In(time.UTC)
				overrideRule, err := rrule.NewRRule(*overrideOptions)
				if err != nil {
					continue
				}
				overrideOccurrences := overrideRule.Between(startOfToday.UTC(), endOfToday.UTC(), true)
				for _, occ := range overrideOccurrences {
					overrideStartTimes[occ.UTC().Format(time.RFC3339Nano)] = struct{}{}
				}
			}
			eventInstances := GenerateEventInstances(event, startOfToday, endOfToday)
			for _, inst := range eventInstances {
				if inst.IsCancelled {
					continue
				}
				localStart := inst.StartTime.In(loc)
				localOcc := localStart
				if _, isOverride := overrideStartTimes[inst.StartTime.UTC().Format(time.RFC3339Nano)]; !isOverride {
					localOcc = time.Date(
						localStart.Year(),
						localStart.Month(),
						localStart.Day(),
						canonicalHour,
						canonicalMinute,
						0,
						0,
						loc,
					)
				}
				if localOcc.Before(startOfToday) || !localOcc.Before(endOfToday) {
					continue
				}
				facilityName := ""
				if class.Facility != nil {
					facilityName = class.Facility.Name
				}
				_, instructorName := GetInstructorFromEvents(class.Events)
				items = append(items, TodaysScheduleItem{
					CohortID:       class.ID,
					ClassName:      class.ClassName,
					InstructorName: instructorName,
					FacilityID:     class.FacilityID,
					FacilityName:   facilityName,
					EventID:        inst.EventID,
					Date:           localOcc.Format("2006-01-02"),
					StartTime:      localOcc.Format("15:04"),
					Room:           inst.Room,
				})
			}
		}
	}

	if len(items) > 0 {
		eventIDs := make([]uint, 0, len(items))
		dates := make([]string, 0, len(items))
		for _, item := range items {
			eventIDs = append(eventIDs, item.EventID)
			dates = append(dates, item.Date)
		}
		attendanceCounts, err := svc.db.GetAttendanceCountsForEvents(args, eventIDs, dates)
		if err == nil {
			taken := make(map[string]bool)
			for _, ac := range attendanceCounts {
				taken[fmt.Sprintf("%d|%s", ac.EventID, ac.Date)] = true
			}
			for i, item := range items {
				items[i].HasAttendance = taken[fmt.Sprintf("%d|%s", item.EventID, item.Date)]
			}
		}

		classIDSet := make(map[uint]struct{}, len(items))
		for _, item := range items {
			classIDSet[item.CohortID] = struct{}{}
		}
		classIDs := make([]uint, 0, len(classIDSet))
		for id := range classIDSet {
			classIDs = append(classIDs, id)
		}
		enrollmentCounts, err := svc.db.GetActiveEnrollmentCountsForClasses(args, classIDs)
		if err == nil {
			for i := range items {
				items[i].EnrolledCount = enrollmentCounts[items[i].CohortID]
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Date == items[j].Date { //sorting by date, time, then by name
			if items[i].StartTime == items[j].StartTime {
				return items[i].ClassName < items[j].ClassName
			}
			return items[i].StartTime < items[j].StartTime
		}
		return items[i].Date < items[j].Date
	})

	return items, nil
}

func (svc *ClassesService) GetProgramClassDetailsForProgram(args *QueryContext, programID int) ([]ProgramClassDetail, error) {
	classes, err := svc.db.GetProgramClassDetailsByID(programID, args)
	if err != nil {
		return nil, err
	}
	if len(classes) == 0 {
		return classes, nil
	}

	classIDs := make([]uint, 0, len(classes))
	for _, cls := range classes {
		classIDs = append(classIDs, cls.ID)
	}
	attendanceByClass, err := svc.db.GetCumulativeAttendanceRatesForClasses(args.Ctx, classIDs)
	if err != nil {
		return nil, err
	}

	for i := range classes {
		schedule, room := formatClassScheduleAndRoom(classes[i].Events, args.Timezone)
		classes[i].Schedule = schedule
		if room != "" {
			classes[i].Room = room
		}
		if rate, ok := attendanceByClass[classes[i].ID]; ok {
			classes[i].AttendanceRate = &rate
		}
	}

	return classes, nil
}

func FormatClassScheduleAndRoom(events []ProgramClassEvent, timezone string) (string, string) {
	return formatClassScheduleAndRoom(events, timezone)
}

func formatClassScheduleAndRoom(events []ProgramClassEvent, timezone string) (string, string) {
	var event *ProgramClassEvent
	if len(events) > 0 {
		event = &events[0]
	}
	if event == nil {
		return "", ""
	}

	roomName := ""
	if event.RoomRef != nil {
		roomName = event.RoomRef.Name
	}

	rule, err := event.GetRRuleWithTimezone(timezone)
	if err != nil {
		return "", roomName
	}
	opts := rule.Options
	if opts.Dtstart.IsZero() {
		return "", roomName
	}

	loc := time.UTC
	if timezone != "" {
		if tz, err := time.LoadLocation(timezone); err == nil {
			loc = tz
		}
	}

	startTime := opts.Dtstart.In(loc)
	duration, err := time.ParseDuration(event.Duration)
	if err != nil {
		return "", roomName
	}
	endTime := startTime.Add(duration)

	days := formatWeekdays(opts.Byweekday, startTime.Weekday())
	timeRange := fmt.Sprintf("%s - %s", startTime.Format("3:04 PM"), endTime.Format("3:04 PM"))
	if len(days) == 0 {
		return timeRange, roomName
	}
	return fmt.Sprintf("%s • %s", strings.Join(days, ", "), timeRange), roomName
}

func formatWeekdays(days []rrule.Weekday, fallback time.Weekday) []string {
	if len(days) == 0 {
		return []string{fullWeekdayName(fallback)}
	}
	unique := make(map[int]rrule.Weekday, len(days))
	for _, day := range days {
		unique[day.Day()] = day
	}
	ordered := make([]rrule.Weekday, 0, len(unique))
	for _, day := range unique {
		ordered = append(ordered, day)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Day() < ordered[j].Day()
	})
	names := make([]string, 0, len(ordered))
	for _, day := range ordered {
		names = append(names, fullWeekdayNameFromRRule(day.Day()))
	}
	return names
}

func fullWeekdayName(day time.Weekday) string {
	switch day {
	case time.Monday:
		return "Monday"
	case time.Tuesday:
		return "Tuesday"
	case time.Wednesday:
		return "Wednesday"
	case time.Thursday:
		return "Thursday"
	case time.Friday:
		return "Friday"
	case time.Saturday:
		return "Saturday"
	case time.Sunday:
		return "Sunday"
	default:
		return ""
	}
}

func fullWeekdayNameFromRRule(day int) string {
	switch day {
	case 0:
		return "Monday"
	case 1:
		return "Tuesday"
	case 2:
		return "Wednesday"
	case 3:
		return "Thursday"
	case 4:
		return "Friday"
	case 5:
		return "Saturday"
	case 6:
		return "Sunday"
	default:
		return ""
	}
}
