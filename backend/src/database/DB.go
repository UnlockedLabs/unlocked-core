package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/glebarez/sqlite"
	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB wraps a GORM database connection for application database operations.
type DB struct{ *gorm.DB }

// NewDB creates a DB wrapper around an existing GORM database connection.
func NewDB(db *gorm.DB) *DB {
	return &DB{db}
}

// ValidateAlphaNumSpace reports whether a field contains only letters, digits, spaces, or hyphens.
func ValidateAlphaNumSpace(fl validator.FieldLevel) bool {
	for _, char := range fl.Field().String() {
		if !unicode.IsDigit(char) && !unicode.IsLetter(char) && !unicode.IsSpace(char) && char != '-' && char != '.' {
			return false
		}
	}
	return true
}

// ValidateAlphaNumPeriod backs the `alphanumperiod` tag on Username. The
// built-in `alphanumunicode` it replaced rejected periods, which would have
// failed a username the handler had already accepted.
func ValidateAlphaNumPeriod(fl validator.FieldLevel) bool {
	for _, char := range fl.Field().String() {
		if !unicode.IsDigit(char) && !unicode.IsLetter(char) && char != '.' {
			return false
		}
	}
	return true
}

// Validate provides a shared validator instance with custom application validations registered.
var Validate = sync.OnceValue(func() *validator.Validate {
	Ins := validator.New(validator.WithRequiredStructEnabled())
	err := Ins.RegisterValidation("alphanumspace", ValidateAlphaNumSpace, false)
	if err != nil {
		logrus.Fatalf("Failed to register custom validation: %v", err)
	}
	err = Ins.RegisterValidation("alphanumperiod", ValidateAlphaNumPeriod, false)
	if err != nil {
		logrus.Fatalf("Failed to register custom validation: %v", err)
	}
	return Ins
})

// PostgresDSN resolves the Postgres connection string from the environment:
// APP_DSN if set, else the individual DB_* vars. Shared by the server
// (InitDB) and the standalone migration runner so the connection rule has one
// home; sslMode is a parameter rather than part of the shared rule because
// the two callers intentionally use different values.
func PostgresDSN(sslMode string) string {
	if dsn := os.Getenv("APP_DSN"); dsn != "" {
		return dsn
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"), sslMode)
}

// InitDB initializes the database connection and seeds default data. Migrations
// run separately (a Kubernetes Job before deploy in production; MigrateTesting
// for the in-memory SQLite test path). On Postgres it first waits until the
// database is migrated, and returns nil if ctx is canceled before then.
func InitDB(ctx context.Context, isTesting bool) *DB {
	var gormDb *gorm.DB
	var err error
	if isTesting {
		dbName := fmt.Sprintf("file:memdb_%d?mode=memory&cache=shared", time.Now().UnixNano())
		gormDb, err = gorm.Open(sqlite.Open(dbName), &gorm.Config{
			DisableForeignKeyConstraintWhenMigrating: true,
		})
		if err != nil {
			logrus.Fatal("Failed to connect to SQLite database:", err)
		}
		logrus.Println("Connected to the SQLite database in memory")
		MigrateTesting(gormDb)
	} else {
		db := openPostgres()
		if !WaitForMigrations(ctx, db) {
			return nil
		}
		gormDb = openGorm(db)
	}
	DB := &DB{gormDb}
	DB.SeedDefaultData(isTesting)

	return DB
}

// Connect opens the PostgreSQL database without waiting for migrations or
// seeding default data. It's for processes that run beside the API server, such
// as the scheduler, which wait for the migration runner themselves.
func Connect() *DB {
	return &DB{openGorm(openPostgres())}
}

// LatestCoreMigration is the newest core migration in MIGRATION_DIR. A folder
// that can't be read or has no migrations is an error: the image or its
// config is wrong, and waiting won't fix it.
func LatestCoreMigration() (int64, error) {
	files, err := goose.CollectMigrations(migrationDir(), 0, goose.MaxVersion)
	if err != nil {
		return 0, fmt.Errorf("list core migrations: %w", err)
	}
	return files[len(files)-1].Version, nil
}

// CoreMigrationsPending reports whether the database is behind latest, the
// newest core migration. It only reads; see module.AppliedVersion.
func CoreMigrationsPending(ctx context.Context, db *sql.DB, latest int64) (bool, error) {
	applied, err := module.AppliedVersion(ctx, db, "goose_db_version")
	if err != nil {
		return false, err
	}
	return applied < latest, nil
}

// migrationPollInterval is how often a caller waiting on WaitForMigrations
// checks whether the database has caught up with this image's migrations.
const migrationPollInterval = 5 * time.Second

// WaitForMigrations blocks until the database has every core and module
// migration this image ships with. Starting earlier would query tables or
// columns that don't exist yet -- a partially-migrated database previously
// let the server start quietly and run new code against an old schema, and a
// not-yet-migrated one crashed with "relation does not exist" instead of
// waiting. Shared by InitDB and the scheduler process so both wait the same
// way. Errors are waited out: the goose ledger doesn't exist until the first
// migration run. Returns false if ctx is canceled first.
func WaitForMigrations(ctx context.Context, db *sql.DB) bool {
	latest, err := LatestCoreMigration()
	if err != nil {
		logrus.Fatalf("Failed to read core migrations: %v", err)
	}
	mods := registry.All()
	for {
		pending, err := CoreMigrationsPending(ctx, db, latest)
		if err == nil && !pending {
			pending, err = module.Pending(ctx, db, goose.DialectPostgres, mods)
		}
		switch {
		case err != nil:
			logrus.Warnf("Waiting for migrations: %v", err)
		case pending:
			logrus.Info("Waiting for migrations to finish")
		default:
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(migrationPollInterval):
		}
	}
}

func migrationDir() string {
	if dir := os.Getenv("MIGRATION_DIR"); dir != "" {
		return dir
	}
	return "migrations"
}

func openPostgres() *sql.DB {
	db, err := sql.Open("pgx", PostgresDSN("allow"))
	if err != nil {
		logrus.Fatalf("Failed to open database connection: %v", err)
	}
	if err := db.Ping(); err != nil {
		logrus.Fatalf("Failed to ping database: %v", err)
	}
	return db
}

// ModuleDBs is the database connection of each feature module.
type ModuleDBs struct {
	core *gorm.DB
	own  map[string]*gorm.DB
}

// OpenModuleDBs opens a pool for each of mods that has its own schema
// (module.Schema), with search_path set to the schema, then public. A process
// opens them once, at startup. The SQLite tests have no schemas, so every
// module shares core's connection there.
func OpenModuleDBs(core *gorm.DB, mods []module.Module) ModuleDBs {
	dbs := ModuleDBs{core: core, own: map[string]*gorm.DB{}}
	if core.Name() != "postgres" {
		return dbs
	}
	for _, m := range mods {
		if s, ok := m.(module.Schema); ok {
			dbs.own[m.Name()] = openGorm(openModulePostgres(s.Schema()))
		}
	}
	return dbs
}

// For is the connection the module called name uses: its own when it has a
// schema, else core's.
func (dbs ModuleDBs) For(name string) *gorm.DB {
	if db, ok := dbs.own[name]; ok {
		return db
	}
	return dbs.core
}

// openModulePostgres opens a pool on core's database whose sessions look up
// tables in schema first, then public.
func openModulePostgres(schema string) *sql.DB {
	cfg, err := pgx.ParseConfig(PostgresDSN("allow"))
	if err != nil {
		logrus.Fatalf("Failed to parse the database connection string: %v", err)
	}
	cfg.RuntimeParams["search_path"] = schema + ", public"
	db := stdlib.OpenDB(*cfg)
	if err := db.Ping(); err != nil {
		logrus.Fatalf("Failed to ping database for schema %s: %v", schema, err)
	}
	return db
}

func openGorm(db *sql.DB) *gorm.DB {
	slowThreshold := 200 * time.Millisecond
	if ms := os.Getenv("GORM_SLOW_THRESHOLD_MS"); ms != "" {
		const maxDurationMilliseconds = int64(1<<63-1) / int64(time.Millisecond)
		if parsed, err := strconv.Atoi(ms); err == nil && parsed > 0 {
			if parsed, err := strconv.ParseInt(ms, 10, 64); err == nil &&
				parsed > 0 && parsed <= maxDurationMilliseconds {
				slowThreshold = time.Duration(parsed) * time.Millisecond
			}
		}
	}
	// Configure GORM to log only errors and slow queries; threshold from GORM_SLOW_THRESHOLD_MS.
	gormDb, err := gorm.Open(postgres.New(postgres.Config{
		Conn: db,
	}), &gorm.Config{
		Logger: newLogrusGormLogger(logger.Config{
			LogLevel:                  logger.Warn,
			SlowThreshold:             slowThreshold,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		}),
	})
	if err != nil {
		logrus.Fatalf("Failed to connect to PostgreSQL database using GORM: %v", err)
	}
	logrus.Println("Connected to the PostgreSQL database via GORM")
	return gormDb
}

// MigrateTesting migrates the in-memory SQLite schema used during tests.
func MigrateTesting(db *gorm.DB) {
	var TableList = []any{
		&models.Role{},
		&models.User{},
		&models.LoginMetrics{},
		&models.LoginActivity{},
		&models.FailedLoginAttempts{},
		// ⚠️  Hand-maintained. An omission here fails at RUNTIME (missing table in the
		//     in-memory SQLite test DB), not at compile time.
		&models.Facility{},
		&models.CronJob{},
		&models.RunnableTask{},
		&models.UserAccountHistory{},
		&models.FeatureFlags{},
		&models.PageFeatureFlags{},
		&models.FacilityFeatureFlag{},
	}
	logrus.Println("Running up migrations...")
	for _, table := range append(TableList, moduleTestModels(registry.All())...) {
		logrus.Printf("Migrating %T table...", table)
		if err := db.AutoMigrate(table); err != nil {
			logrus.Fatal("Failed to migrate table: ", err)
		}
	}

}

// moduleTestModels lists the models of every module that has them, in module
// order.
func moduleTestModels(mods []module.Module) []any {
	var tables []any
	for _, m := range mods {
		if tm, ok := m.(module.TestModels); ok {
			tables = append(tables, tm.TestModels()...)
		}
	}
	return tables
}

// seedModules lets every module with default rows add them to a new
// database, as the user in ctx.
func seedModules(ctx context.Context, db *gorm.DB, facilityID uint, mods []module.Module) error {
	for _, m := range mods {
		if s, ok := m.(module.Seeder); ok {
			if err := s.Seed(ctx, db, facilityID); err != nil {
				return fmt.Errorf("seed module %s: %w", m.Name(), err)
			}
		}
	}
	return nil
}

// SeedDefaultData creates the default administrative user, facility, links, and content providers.
func (db *DB) SeedDefaultData(isTesting bool) {
	var count int64
	if err := db.Model(models.User{}).Where("id = ?", 1).Count(&count).Error; err != nil {
		logrus.Fatal("db transaction failed getting admin user")
	}
	if count == 0 {
		if err := db.Model(models.Facility{}).Where("id = ?", 1).Count(&count).Error; err != nil {
			logrus.Fatal("db transaction failed getting default facility")
		}
		if isTesting {
			roles := []models.Role{{Name: "admin"}, {Name: "student"}, {Name: "system_admin"}}
			for _, role := range roles {
				if err := db.Create(&role).Error; err != nil {
					logrus.Fatalf("Failed to create role: %v", err)
				}
			}
			db.seedTestFeatureFlags()
		}

		// Get system_batch user for audit field references
		var systemBatchUser models.User
		systemBatchUserID := uint(99999)
		if err := db.Where("username = ?", "system_batch").First(&systemBatchUser).Error; err != nil {
			logrus.Printf("Warning: system_batch user not found, this might indicate migration hasn't run yet: %v", err)
		} else {
			systemBatchUserID = systemBatchUser.ID
		}
		dbWithCtx := db.WithContext(context.WithValue(context.Background(), models.UserIDKey, systemBatchUserID))

		user := models.User{
			Username:   "SuperAdmin",
			NameFirst:  "Super",
			NameLast:   "Admin",
			Email:      "admin@unlocked.v2",
			Role:       models.SystemAdmin,
			FacilityID: 0, // will update after facility creation
			DatabaseFields: models.DatabaseFields{
				CreateUserID: &systemBatchUserID, // using system_batch user ID
			},
		}
		logrus.Printf("Creating user: %v", user)
		logrus.Println("Make sure to sync the Kratos instance if you are freshly migrating")
		if err := dbWithCtx.Create(&user).Error; err != nil {
			logrus.Fatalf("Failed to create user: %v", err)
		}

		// Step 2: Create facility referencing the user we just created
		defaultFacility := models.Facility{
			Name:     "Default",
			Timezone: "America/Chicago",
			DatabaseFields: models.DatabaseFields{
				CreateUserID: &user.ID,
			},
		}
		logrus.Printf("Creating facility: %v", defaultFacility)
		if err := dbWithCtx.Create(&defaultFacility).Error; err != nil {
			logrus.Fatalf("Failed to create facility: %v", err)

		}

		if err := db.Model(&user).Update("facility_id", defaultFacility.ID).Error; err != nil {
			logrus.Fatalf("Failed to update user facility_id: %v", err)
		}

		seedCtx := identity.NewContext(context.Background(), &identity.Claims{UserID: systemBatchUserID})
		if err := seedModules(seedCtx, db.DB, defaultFacility.ID, registry.All()); err != nil {
			logrus.Fatalf("Failed to seed modules: %v", err)
		}
	}
}

func (db *DB) softDeleteMap() map[string]any {
	updates := map[string]any{
		"deleted_at": time.Now(),
	}
	if userID, ok := db.Statement.Context.Value(models.UserIDKey).(uint); ok {
		updates["update_user_id"] = userID
	}
	return updates
}
