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
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	"github.com/go-faker/faker/v4"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	SystemBatchID  = 99999
	SystemBatchUID = "system_batch"
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
	// Core's rows read the user who created them from models.UserIDKey.
	db = db.WithContext(context.WithValue(context.Background(), models.UserIDKey, systemUserID))

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
