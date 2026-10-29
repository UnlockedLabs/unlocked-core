// Package module is the seam between core and the feature modules in
// src/modules. A feature module may import this package, other modules' api
// packages and unlocked-contracts, and nothing else from core; depguard in
// backend/.golangci.yml enforces it.
package module

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"

	"github.com/nats-io/nats.go"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
)

// Module is one feature. Core calls Register and RegisterJobs once at startup
// and runs the module's migrations after its own. RegisterJobs can run in a
// different process from Register (the scheduler, see tasks.Start), so routes
// and jobs share state only through the database or NATS.
type Module interface {
	// Name is the module's folder name. It also names the module's migration
	// ledger, so it never changes.
	Name() string
	Register(r Router, deps Deps)
	RegisterJobs(s Scheduler, deps Deps)
	// Migrations holds the module's goose .sql files in a "migrations" folder.
	Migrations() fs.FS
}

// Route is one HTTP route, such as "GET /api/libraries". The module writes
// its own responses and errors (EN-188: handler helpers stay in each module).
type Route struct {
	Pattern string
	Handler http.Handler
	// Access is who may call the route. The zero value is AnyUser.
	Access AccessLevel
	// UserParam names the path parameter that holds the user a route is about,
	// such as "id" in "GET /api/users/{id}/courses". Then only admins who can
	// see that user may call the route: department and system admins see
	// everyone, facility admins their own facility's users. With AnyUser, the
	// user themself may too. It works only with AnyUser and Admin.
	UserParam string
	// Allow is the module's own access check, for checks Access and UserParam
	// can't express, such as a class in the caller's facility. It runs after
	// them, and the caller needs to pass every check. A caller it denies gets
	// the same 401 as core's own checks give.
	Allow func(r *http.Request, c *identity.Claims) bool
	// Features turns the route off unless every listed feature is enabled.
	Features []features.Access
}

// AccessLevel is who may call a route. Core enforces each level with the same
// checks as its own routes.
type AccessLevel int

const (
	// AnyUser is any logged-in user.
	AnyUser AccessLevel = iota
	// Admin is any admin: facility, department or system.
	Admin
	// DeptAdmin is department and system admins: the admins who can switch
	// facility.
	DeptAdmin
	// SystemAdmin is system admins only.
	SystemAdmin
)

// Router registers a module's routes behind core's auth, admin and
// feature-flag checks.
type Router interface {
	Handle(Route)
}

// Scheduler runs a module's background jobs.
type Scheduler interface {
	// Cron runs task on a standard five-field cron schedule.
	Cron(spec string, task func()) error
	// AtStart runs task once, a minute after the scheduler starts, so the
	// workers that start alongside it are listening.
	AtStart(task func()) error
}

// Worker is a module with background work of its own, such as running the
// jobs it publishes. Core runs it as a separate process, `backend worker
// <name>`, with the same Deps as its jobs. Work returns when ctx is canceled.
type Worker interface {
	Work(ctx context.Context, deps Deps) error
}

// Deps is what core shares with every module, for both its routes and its
// jobs.
type Deps struct {
	DB   *gorm.DB
	NATS *nats.Conn
	// Notify sends messages to a user's open browser tabs.
	Notify Notifier
	// Users is core's user accounts. Only routes get it: it is nil in
	// RegisterJobs.
	Users Users
	// LiveProviders is Programs' Canvas and Essential Education code, for
	// Provider Platforms. Only routes get it. It goes away when Provider
	// Platforms fetches and caches those providers' data itself.
	LiveProviders LiveProviders
	// HTTP is core's HTTP client, for calls to services outside Unlocked,
	// such as an LMS. Only routes get it.
	HTTP HTTPClient
	// Features is core's feature flags for each facility. Only routes get it.
	Features Features
	// Reports renders reports as PDFs with core's report templates. Only
	// routes get it.
	Reports Reports
	// ModuleDBs is each module's connection, by module name
	// (database.ModuleDBs.For), for the registry to build another module's api
	// on that module's own connection. A module never queries through it.
	// Only routes get it.
	ModuleDBs func(name string) *gorm.DB
}

// Notifier sends a message to a user over core's websocket. A user with no
// open tab misses it.
type Notifier interface {
	Notify(userID uint, msg Message)
}

// Message is one websocket message to a user.
type Message struct {
	// Event is the message's type, such as "visits".
	Event string
	// ActivityID is the Open Content activity the message is about, if any.
	// Core ends that activity when the user's tab closes.
	ActivityID int64
	Text       string
}

// Users is core's user accounts, for a module that adds or looks up users.
type Users interface {
	// Get returns the user with id. When there is none, the error matches
	// gorm.ErrRecordNotFound.
	Get(ctx context.Context, id uint) (User, error)
	// Create adds a user, recording the caller in ctx as its creator. The user
	// can't log in until CreateLogin.
	Create(ctx context.Context, u NewUser) (User, error)
	// CreateLogin gives a user a Kratos login with a new temporary password,
	// which they must change when they first log in. It returns the password.
	CreateLogin(ctx context.Context, userID uint) (string, error)
	// Delete removes a user and their links to provider platform accounts,
	// such as to undo a Create. It doesn't remove their Kratos login.
	Delete(ctx context.Context, id uint) error
}

// HTTPClient sends a request with core's HTTP client. *http.Client is one.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Features is core's feature flags.
type Features interface {
	// FacilityFeatures is the features turned on at a facility: its own
	// settings, else the statewide ones, leaving out a sub-feature whose
	// parent is off.
	FacilityFeatures(ctx context.Context, facilityID uint) ([]features.Access, error)
}

// Reports renders reports with core's PDF report templates.
type Reports interface {
	// PDF is report rendered with the named template, such as
	// "attendance_report".
	PDF(template string, report Report) ([]byte, error)
}

// Report is the table a PDF report shows.
type Report struct {
	Title   string
	Rows    [][]string
	SubRows [][]string
	// Params are extra template parameters, such as which columns to show.
	// Set only those the template declares.
	Params map[string]string
	// Filters are the filters the report was run with, shown in its header.
	Filters []ReportFilter
}

// ReportFilter is one filter in a report's header, such as "Facility".
type ReportFilter struct {
	Label string
	Value string
}

// LiveProviders is what Provider Platforms needs from Programs' Canvas and
// Essential Education code.
type LiveProviders interface {
	// Users is every user a provider's own API lists. ok is false for a
	// provider that can't list them; its users come from Provider Platforms'
	// worker.
	Users(ctx context.Context, providerID uint) (users []ProviderUser, ok bool, err error)
	// MappingsChanged tells Programs that a user's account at a provider was
	// linked or unlinked, so it can clear its cached Canvas programs.
	MappingsChanged(providerID, userID uint)
}

// ProviderUser is a user's account at a provider platform.
type ProviderUser struct {
	Username         string
	NameFirst        string
	NameLast         string
	Email            string
	ExternalUserID   string
	ExternalUsername string
}

// User is what a module sees of a user.
type User struct {
	ID         uint
	Username   string
	NameFirst  string
	NameLast   string
	Email      string
	FacilityID uint
}

// NewUser is a user for Users.Create to add.
type NewUser struct {
	Username   string
	NameFirst  string
	NameLast   string
	Email      string
	FacilityID uint
	Role       users.Role
}

// Schema is a module whose tables live in its own Postgres schema. Core
// connects its routes, jobs and worker as the Postgres role of the same name,
// with search_path set to the schema, then public, so its queries find its own
// tables first, then core's. The module's first migration creates the schema
// and the role, and grants the role only what the module needs.
type Schema interface {
	Schema() string
}

// RunAs runs fn on tx, a transaction on core's connection, as module m's own
// connection would: on Postgres, when m has its own schema, under m's role and
// with m's schema first on the search_path. Both go back to core's when fn
// returns, so the rest of the transaction runs as core. Core uses it to make a
// module's writes part of one of its transactions. When fn fails, the
// transaction is aborted and left to the caller's rollback.
func RunAs(tx *gorm.DB, m Module, fn func(tx *gorm.DB) error) error {
	s, ok := m.(Schema)
	if !ok || tx.Name() != "postgres" {
		return fn(tx)
	}
	role := `"` + strings.ReplaceAll(s.Schema(), `"`, `""`) + `"`
	for _, stmt := range []string{"SET LOCAL ROLE " + role, "SET LOCAL search_path = " + role + ", public"} {
		if err := tx.Exec(stmt).Error; err != nil {
			return fmt.Errorf("run as module %s: %w", m.Name(), err)
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	for _, stmt := range []string{"RESET ROLE", "RESET search_path"} {
		if err := tx.Exec(stmt).Error; err != nil {
			return fmt.Errorf("run as module %s: %w", m.Name(), err)
		}
	}
	return nil
}

// Seeder is a module that adds default rows to a new database. Core calls
// Seed once, right after it creates the default facility. ctx carries the
// identity.Claims of the user the rows are created by.
type Seeder interface {
	Seed(ctx context.Context, db *gorm.DB, facilityID uint) error
}

// TestModels is a module whose tables core's SQLite test database creates
// with AutoMigrate. Production creates them with migrations.
type TestModels interface {
	TestModels() []any
}

type logKey struct{}

// requestLog is core's log for one module request.
type requestLog struct {
	fields map[string]any
	add    func(key string, value any)
}

// WithLog returns a copy of ctx that carries core's log for the request:
// fields is what core has logged so far, such as the admin on an admin write,
// and add adds a field to core's log lines, including the admin audit line.
// Core sets it on every module request.
func WithLog(ctx context.Context, fields map[string]any, add func(key string, value any)) context.Context {
	return context.WithValue(ctx, logKey{}, requestLog{fields: fields, add: add})
}

// LogFields is a copy of core's log fields for the request, for a module's own
// log lines to start from. Outside a module request it's empty.
func LogFields(ctx context.Context) map[string]any {
	rl, _ := ctx.Value(logKey{}).(requestLog)
	fields := make(map[string]any, len(rl.fields))
	maps.Copy(fields, rl.fields)
	return fields
}

// LogField adds a field, such as the library a request acted on, to core's
// log lines for the request. Outside a module request it does nothing.
func LogField(ctx context.Context, key string, value any) {
	if rl, ok := ctx.Value(logKey{}).(requestLog); ok {
		rl.add(key, value)
	}
}

// AuditAction names what an admin's request did, such as "video_featured",
// in core's admin audit log.
func AuditAction(ctx context.Context, action string) {
	LogField(ctx, "action", action)
}

const migrationsDir = "migrations"

// LedgerTable is the goose version table for a module's migrations. Each
// module has its own, so module versions never collide with core's
// (public.goose_db_version) or with each other's.
func LedgerTable(name string) string {
	return "goose_db_version_" + name
}

// Migrate runs each module's migrations, in order. Call it after core's
// migrations. A module with no migrations is skipped.
func Migrate(ctx context.Context, db *sql.DB, dialect goose.Dialect, mods []Module) error {
	for _, m := range mods {
		p, err := provider(db, dialect, m)
		if errors.Is(err, goose.ErrNoMigrations) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := p.Up(ctx); err != nil {
			return fmt.Errorf("migrate module %s: %w", m.Name(), err)
		}
	}
	return nil
}

// Reset rolls back every module's migrations, in reverse order. Call it
// before resetting core's, because module tables can reference core's.
func Reset(ctx context.Context, db *sql.DB, dialect goose.Dialect, mods []Module) error {
	for _, m := range slices.Backward(mods) {
		p, err := provider(db, dialect, m)
		if errors.Is(err, goose.ErrNoMigrations) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := p.DownTo(ctx, 0); err != nil {
			return fmt.Errorf("reset module %s: %w", m.Name(), err)
		}
	}
	return nil
}

// Pending reports whether any module has a migration its ledger doesn't record
// as applied. It only reads, so a process that doesn't run migrations (the
// scheduler) can use it to wait for the one that does.
func Pending(ctx context.Context, db *sql.DB, dialect goose.Dialect, mods []Module) (bool, error) {
	for _, m := range mods {
		p, err := provider(db, dialect, m)
		if errors.Is(err, goose.ErrNoMigrations) {
			continue
		}
		if err != nil {
			return false, err
		}
		sources := p.ListSources()
		applied, err := AppliedVersion(ctx, db, LedgerTable(m.Name()))
		if err != nil {
			return false, fmt.Errorf("module %s: %w", m.Name(), err)
		}
		if applied < sources[len(sources)-1].Version {
			return true, nil
		}
	}
	return false, nil
}

// AppliedVersion is the highest version a goose ledger table records as
// applied. It only reads: unlike goose's own checks, it never creates the
// table, so a missing ledger is an error.
func AppliedVersion(ctx context.Context, db *sql.DB, ledger string) (int64, error) {
	var v int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM `+ledger+` WHERE is_applied`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("read migration ledger %s: %w", ledger, err)
	}
	return v, nil
}

func provider(db *sql.DB, dialect goose.Dialect, m Module) (*goose.Provider, error) {
	fsys, err := fs.Sub(m.Migrations(), migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("module %s migrations: %w", m.Name(), err)
	}
	p, err := goose.NewProvider(dialect, db, fsys,
		goose.WithTableName(LedgerTable(m.Name())),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil && !errors.Is(err, goose.ErrNoMigrations) {
		return nil, fmt.Errorf("module %s migrations: %w", m.Name(), err)
	}
	return p, err
}
