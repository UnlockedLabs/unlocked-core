package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/go-faker/faker/v4"
	"github.com/go-faker/faker/v4/pkg/options"
	"github.com/joho/godotenv"
	"github.com/teambition/rrule-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	DaysInPast     = 90
	MonthsInFuture = 6
	SystemBatchID  = 99999
	SystemBatchUID = "system_batch"
)

var (
	startDate = time.Now().AddDate(0, 0, -DaysInPast)
	endDate   = time.Now().AddDate(0, MonthsInFuture, 0)
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Error loading .env file: %v", err)
	}
	// The seeder writes core's tables and the modules', and a module with its
	// own schema keeps its tables there, so its connection looks in all of them.
	schemas := []string{"public"}
	for _, m := range registry.All() {
		if s, ok := m.(module.Schema); ok {
			schemas = append(schemas, s.Schema())
		}
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=prefer search_path=%s",
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"), strings.Join(schemas, ","))
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: dsn,
	}), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to the PostgreSQL database: %v", err)
	}
	seedTestData(db)
}

func seedTestData(db *gorm.DB) {
	// isTesting is false because this needs to seed real users w/ kratos
	testServer := handlers.NewServer(false, context.Background())
	var systemUser models.User
	systemUserID := uint(SystemBatchID)
	if err := db.Where("username = ?", SystemBatchUID).First(&systemUser).Error; err == nil {
		systemUserID = systemUser.ID
	}
	// Core's rows read the user from models.UserIDKey, the modules' from
	// identity.Claims.
	ctx := context.WithValue(context.Background(), models.UserIDKey, systemUserID)
	db = db.WithContext(identity.NewContext(ctx, &identity.Claims{UserID: systemUserID}))

	facilities := []models.Facility{
		{
			Name:     "BCF",
			Timezone: "America/New_York",
		},
		{
			Name:     "Potosi Correctional Facility",
			Timezone: "America/Chicago",
		},
	}
	for idx := range facilities {
		if err := db.Create(&facilities[idx]).Error; err != nil {
			log.Printf("Failed to create facility: %v", err)
		}
	}
	facilities = []models.Facility{}
	err := db.Find(&facilities).Error
	if err != nil {
		log.Printf("Failed to get facilities: %v", err)
	}
	users := generateFakeUsers(facilities)
	for idx := range users {
		log.Printf("Creating user %s", users[idx].Username)
		if err := db.Create(&users[idx]).Error; err != nil {
			log.Printf("Failed to create user: %v", err)
		}
		if err := testServer.HandleCreateUserKratos(users[idx].Username, "ChangeMe!"); err != nil {
			log.Fatalf("unable to create test user in kratos")
		}
		// No ProviderUserMapping is seeded on purpose. A mapping with a made-up
		// external_user_id makes a resident look linked to a provider account that does not
		// exist, and GetUnmappedUsers then hides them from the list you would use to link
		// them for real. Link users by hand against a live provider instead.
	}
	cohorts := []programs.ProgramClassCohort{}
	cohorts, err = createFacilityPrograms(db)
	if err != nil {
		log.Printf("Failed to create facility programs: %v", err)
	}
	events := []programs.ProgramClassEvent{}
	if err := db.Find(&events).Error; err != nil {
		log.Fatalf("Failed to get events from db")
	}
	// class_completions.admin_email is NOT NULL and every certificate screen shows it, so
	// resolve a real granting admin once. generateFakeUsers only makes students, so this
	// comes from the accounts created during initial setup.
	adminEmail := "admin@unlocked.test"
	var seededAdminEmail string
	if err := db.Model(&models.User{}).
		Where("role IN ?", models.AdminRoles).
		Order("id").
		Limit(1).
		Pluck("email", &seededAdminEmail).Error; err == nil && seededAdminEmail != "" {
		adminEmail = seededAdminEmail
	}
	for _, user := range users {
		statuses := [6]programs.ProgramEnrollmentStatus{
			programs.EnrollmentCancelled,
			programs.EnrollmentCompleted,
			programs.EnrollmentIncompleteWithdrawn,
			programs.EnrollmentIncompleteDropped,
			programs.EnrollmentIncompleteFailedToComplete,
			programs.EnrollmentIncompleteTransfered,
		}

		numEnrollments := rand.Intn(3) + 1
		availableCohorts := []programs.ProgramClassCohort{}
		for i := range cohorts {
			if cohorts[i].FacilityID == user.FacilityID {
				availableCohorts = append(availableCohorts, cohorts[i])
			}
		}
		rand.Shuffle(len(availableCohorts), func(i, j int) {
			availableCohorts[i], availableCohorts[j] = availableCohorts[j], availableCohorts[i]
		})
		seenClasses := make(map[uint]bool, len(availableCohorts))
		sampled := make([]programs.ProgramClassCohort, 0, numEnrollments)
		for _, cohort := range availableCohorts {
			if seenClasses[cohort.ClassID] {
				continue
			}
			seenClasses[cohort.ClassID] = true
			sampled = append(sampled, cohort)
			if len(sampled) == numEnrollments {
				break
			}
		}
		for _, cohort := range sampled {
			status := programs.Enrolled
			if rand.Intn(100)%2 == 0 {
				status = statuses[rand.Intn(len(statuses))]
			}
			// CohortID is what a resident enrolls in; the BeforeCreate hook denormalizes
			// the parent ClassID from it. Setting ClassID here would store a COHORT id
			// in the class column -- it compiles, and it corrupts the class rollups.
			enrollment := programs.ProgramClassEnrollment{
				UserID:           user.ID,
				CohortID:         cohort.ID,
				EnrollmentStatus: status,
			}
			if err := db.Create(&enrollment).Error; err != nil {
				log.Printf("Failed to create enrollment: %v", err)
			}
			if status == programs.EnrollmentCompleted {
				cohortID, classID, programID := cohort.ID, cohort.ClassID, cohort.ProgramID
				// The enrollment hook only sets enrolled_at for an Active cohort; seeded
				// cohorts are Scheduled, so fall back to the cohort's start date rather
				// than leaving the zero time (which renders as year 0001).
				enrolledOn := cohort.StartDt
				if enrollment.EnrolledAt != nil {
					enrolledOn = *enrollment.EnrolledAt
				}
				completion := programs.ClassCompletion{
					UserID:        user.ID,
					CohortID:      &cohortID,
					ClassID:       &classID,
					ProgramID:     &programID,
					ProgramName:   faker.Sentence(options.WithRandomStringLength(16)),
					FacilityName:  facilities[user.FacilityID-1].Name,
					ProgramOwner:  faker.Name(),
					ClassName:     cohort.ClassName,
					CreditType:    string(resolveCreditType(db, classID, programID)),
					AdminEmail:    adminEmail,
					CohortStartDt: cohort.StartDt,
					EnrolledOnDt:  enrolledOn,
				}
				if err := db.Create(&completion).Error; err != nil {
					log.Printf("Failed to create completion")
				}
			}
			log.Printf("Creating program enrollment for user %s", user.Username)
		}
		attendanceStatus := []programs.Attendance{
			programs.Present,
			programs.Absent_Excused,
			programs.Absent_Unexcused,
		}
		for _, event := range events {
			rule, err := rrule.StrToRRule(event.RecurrenceRule)
			if err != nil {
				log.Printf("Failed to parse rrule for event %d: %v", event.ID, err)
				continue
			}
			occurrences := rule.Between(startDate, endDate, true)
			for _, occ := range occurrences {
				if rand.Intn(10)%2 == 0 {
					continue
				}
				attendanceDate := occ.Format("2006-01-02")
				attendance := programs.ProgramClassEventAttendance{
					EventID:          event.ID,
					UserID:           user.ID,
					Date:             attendanceDate,
					AttendanceStatus: attendanceStatus[rand.Intn(len(attendanceStatus))],
				}

				result := db.Where(attendance).FirstOrCreate(&attendance)
				if result.Error != nil {
					log.Printf("Failed to create attendance for user %d on %s: %v", user.ID, attendance.Date, result.Error)
				} else if result.RowsAffected == 0 {
					log.Printf("Attendance already exists for user %d on %s for event %d. Skipping.", user.ID, attendanceDate, event.ID)
				} else {
					log.Printf("Created attendance for user %s at event: %d on %s", user.Username, event.ID, attendanceDate)
				}
			}
		}
	}
	createUserSessionActivity(db, users)
}

func createUserSessionActivity(db *gorm.DB, dbUsers []models.User) {
	now := time.Now()
	threeMonthsInPast := now.AddDate(0, -3, 0)
	for _, user := range dbUsers {
		burstCount := rand.Intn(6) + 3 // 3–8 session bursts
		for range burstCount {
			base := threeMonthsInPast.Add(time.Duration(rand.Intn(90*24)) * time.Hour)
			sessionsInBurst := rand.Intn(4) + 1
			for j := range sessionsInBurst {
				start := base.Add(time.Duration(j*2) * time.Hour)
				duration := time.Duration(rand.Intn(60)+15) * time.Minute
				end := start.Add(duration)
				userSessionTracking := models.UserSessionTracking{
					UserID:         user.ID,
					SessionStartTS: start,
					SessionEndTS:   end,
					SessionID:      fmt.Sprintf("%s-%d", start.Format("20060102"), rand.Intn(10000)),
				}
				if err := db.Create(&userSessionTracking).Error; err != nil {
					log.Printf("Failed to create userSessionTracking: %v", err)
				}
			}
		}
	}
}

func getRandomProgram(programMap map[string]programs.ProgType) string {
	keySlice := make([]string, 0, len(programMap))
	for key := range programMap {
		keySlice = append(keySlice, key)
	}
	return keySlice[rand.Intn(len(keySlice))]
}

func createFacilityPrograms(db *gorm.DB) ([]programs.ProgramClassCohort, error) {
	facilities := []models.Facility{}
	fundingTypes := [6]programs.FundingType{programs.EduGrants, programs.FederalGrants, programs.InmateWelfare, programs.NonProfitOrgs, programs.Other, programs.StateGrants}
	creditTypes := [4]programs.CreditType{programs.Completion, programs.EarnedTime, programs.Education, programs.Participation}
	programMap := map[string]programs.ProgType{
		"Anger Management":          programs.Therapeutic,
		"Substance Abuse Treatment": programs.MentalHealth,
		"AA/NA":                     programs.MentalHealth,
		"Thinking for a Change":     programs.LifeSkills,
		"A New Freedom":             programs.LifeSkills,
		"Dog Training":              programs.Vocational,
		"A New Path":                programs.Therapeutic,
		"GED/Hi-SET":                programs.Educational,
		"Parenting":                 programs.LifeSkills,
		"Employment":                programs.Vocational,
		"Life Skills":               programs.LifeSkills,
		"Health and Wellness":       programs.MentalHealth,
		"Financial Literacy":        programs.Educational,
		"Computer Skills":           programs.Educational,
	}
	programClassDescriptions := map[string]string{
		"Anger Management":          "Techniques to control and express anger constructively.",
		"Substance Abuse Treatment": "Support and strategies for overcoming addiction.",
		"AA/NA":                     "Peer support groups for alcohol and drug recovery.",
		"Thinking for a Change":     "Cognitive-based program for decision-making and behavior change.",
		"A New Freedom":             "Life skills and personal growth program for better choices.",
		"Dog Training":              "Hands-on program teaching dog training and care skills.",
		"A New Path":                "Therapeutic program focused on personal healing and growth.",
		"GED/Hi-SET":                "Education program to earn a high school equivalency diploma.",
		"Parenting":                 "Guidance and skills for effective parenting strategies.",
		"Employment":                "Job readiness, resume building, and interview preparation.",
		"Life Skills":               "Essential skills for daily living and self-sufficiency.",
		"Health and Wellness":       "Education on physical and mental well-being practices.",
		"Financial Literacy":        "Budgeting, saving, and money management fundamentals.",
		"Computer Skills":           "Basic to advanced computer literacy and digital skills.",
	}
	if err := db.Find(&facilities).Error; err != nil {
		return nil, err
	}
	facilityRooms := make(map[uint][]programs.Room)
	roomNames := []string{
		"Room A", "Room B", "Classroom 1", "Classroom 2",
		"Computer Lab", "Library", "Auditorium", "Conference Room",
	}
	for _, facility := range facilities {
		var rooms []programs.Room
		for _, name := range roomNames {
			room := programs.Room{
				FacilityID: facility.ID,
				Name:       name,
			}
			if err := db.Create(&room).Error; err != nil {
				log.Printf("Failed to create room %s for facility %d: %v", name, facility.ID, err)
				continue
			}
			rooms = append(rooms, room)
		}
		facilityRooms[facility.ID] = rooms
		log.Printf("Created %d rooms for facility %s", len(rooms), facility.Name)
	}
	toReturn := make([]programs.ProgramClassCohort, 0)
	seededPrograms := make([]programs.Program, 0, 7)
	for range 7 {
		seededPrograms = append(seededPrograms, programs.Program{
			Name:        getRandomProgram(programMap),
			FundingType: fundingTypes[rand.Intn(len(fundingTypes))],
		})
	}
	for i := range seededPrograms {
		seededPrograms[i].Description = programClassDescriptions[seededPrograms[i].Name]
		seededPrograms[i].IsActive = true
		if err := db.Create(&seededPrograms[i]).Error; err != nil {
			log.Printf("Failed to create program: %v", err)
		}
		for idx := range facilities {
			capacities := []int64{15, 25, 30, 35, 40, 45}
			endDates := []time.Time{time.Now().Add(20 * 24 * time.Hour), time.Now().Add(25 * 24 * time.Hour), time.Now().Add(30 * 24 * time.Hour), time.Now().Add(35 * 24 * time.Hour)}
			facilityProgram := programs.FacilitiesPrograms{
				ProgramID:    seededPrograms[i].ID,
				FacilityID:   facilities[idx].ID,
				ProgramOwner: models.StringPtr(faker.Name()),
			}
			if err := db.Create(&facilityProgram).Error; err != nil {
				log.Printf("Failed to create facility program: %v", err)
			}
			for range rand.Intn(4) {
				programType := programs.ProgramType{
					ProgramType: programMap[seededPrograms[i].Name],
					ProgramID:   seededPrograms[i].ID,
				}
				if err := db.Create(&programType).Error; err != nil {
					log.Printf("Failed to create program type: %v", err)
				}
				creditType := programs.ProgramCreditType{
					CreditType: creditTypes[rand.Intn(len(creditTypes))],
					ProgramID:  seededPrograms[i].ID,
				}
				if err := db.Create(&creditType).Error; err != nil { //we can do multiple credit types if we want, add this during new development if needed
					log.Printf("Failed to create program credit type: %v", err)
				}
				// One CLASS, then 1-3 COHORTS under it. The multi-cohort shape is the
				// point: a 1:1 seed never exercises a single class-level rollup.
				programClass := programs.ProgramClass{
					Name:        seededPrograms[i].Name,
					Description: models.NilIfBlank(programClassDescriptions[seededPrograms[i].Name]),
					CreditHours: &creditHourOptions[rand.Intn(len(creditHourOptions))],
					FacilityID:  facilities[idx].ID,
					ProgramID:   seededPrograms[i].ID,
				}
				if err := db.Create(&programClass).Error; err != nil {
					log.Printf("Failed to create program class: %v", err)
					continue
				}

				// Sometimes give the class its own credit type, so the
				// empty-means-inherit branch and the override branch both get hit.
				if rand.Intn(3) == 0 {
					if err := db.Create(&programs.ProgramClassCreditType{
						ClassID:    programClass.ID,
						CreditType: creditTypes[rand.Intn(len(creditTypes))],
					}).Error; err != nil {
						log.Printf("Failed to create class credit type: %v", err)
					}
				}

				cohortsForClass := rand.Intn(3) + 1
				for c := range cohortsForClass {
					cohort := programs.ProgramClassCohort{
						ClassID: programClass.ID,
						// Read-only alias, so this is never written -- it just carries the
						// name in memory for the completion records built further down.
						ClassName:   programClass.Name,
						Capacity:    capacities[rand.Intn(len(capacities))],
						Description: programClassDescriptions[seededPrograms[i].Name],
						Status:      programs.Scheduled, //this will change during new class development
						StartDt:     time.Now().Add(time.Duration(14+7*c) * 24 * time.Hour),
						EndDt:       &endDates[rand.Intn(len(endDates))],
						FacilityID:  facilities[idx].ID,
						ProgramID:   seededPrograms[i].ID,
					}
					if err := db.Create(&cohort).Error; err != nil {
						log.Printf("Failed to create program class cohort: %v", err)
						continue
					}
					log.Println("Creating program class cohort ", cohort.ID)
					toReturn = append(toReturn, cohort)
					seedCohortEvents(db, cohort, facilityRooms[facilities[idx].ID])
				}
			}
		}
	}
	return toReturn, nil
}

var creditHourOptions = []int64{5, 10, 15, 20}

// resolveCreditType mirrors the production rule in newClassCompletion: the class's own
// credit types override the program's, and an EMPTY class-level set means inherit. The
// seeder gives only some classes their own credit type, so both branches get exercised.
func resolveCreditType(db *gorm.DB, classID, programID uint) programs.CreditType {
	var creditType programs.CreditType
	if err := db.Model(&programs.ProgramClassCreditType{}).
		Where("class_id = ?", classID).
		Limit(1).
		Pluck("credit_type", &creditType).Error; err == nil && creditType != "" {
		return creditType
	}
	if err := db.Model(&programs.ProgramCreditType{}).
		Where("program_id = ?", programID).
		Limit(1).
		Pluck("credit_type", &creditType).Error; err == nil && creditType != "" {
		return creditType
	}
	return programs.Completion
}

func seedCohortEvents(db *gorm.DB, cohort programs.ProgramClassCohort, rooms []programs.Room) {
	if len(rooms) == 0 || cohort.EndDt == nil {
		return
	}
	days := []rrule.Weekday{rrule.MO, rrule.TU, rrule.WE, rrule.TH, rrule.FR, rrule.SA, rrule.SU}
	// Sample WITHOUT replacement: a duplicate weekday in Byweekday makes the rule
	// generate the same occurrence twice, which then seeds duplicate attendance.
	rand.Shuffle(len(days), func(i, j int) { days[i], days[j] = days[j], days[i] })
	randDays := days[:rand.Intn(3)+1]
	rule, err := rrule.NewRRule(rrule.ROption{
		Freq:      rrule.WEEKLY,
		Dtstart:   cohort.StartDt,
		Until:     *cohort.EndDt,
		Byweekday: randDays,
	})
	if err != nil {
		log.Printf("Failed to create rrule: %v", err)
		return
	}
	roomID := rooms[rand.Intn(len(rooms))].ID
	event := programs.ProgramClassEvent{
		CohortID:       cohort.ID,
		RecurrenceRule: rule.String(),
		RoomID:         &roomID,
		Duration:       "1h0m0s",
	}
	if err := db.Create(&event).Error; err != nil {
		log.Printf("Failed to create event: %v", err)
	}
}

func generateFakeUsers(facilities []models.Facility) []models.User {
	users := make([]models.User, 0, 30)
	for range 30 {
		facility := facilities[rand.Intn(len(facilities))]
		first := faker.FirstName()
		last := faker.LastName()
		username := strings.ToLower(fmt.Sprintf("%s.%s%d", first, last, rand.Intn(1000)))
		email := fmt.Sprintf("%s@unlocked.test", username)
		user := models.User{
			Username:   username,
			NameFirst:  first,
			NameLast:   last,
			Email:      email,
			Role:       "student",
			FacilityID: facility.ID,
			DocID:      models.StringPtr(strconv.Itoa(rand.Intn(100000))),
		}
		users = append(users, user)
	}
	return users
}
