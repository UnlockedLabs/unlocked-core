package integration

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

type TestEnv struct {
	Server     *handlers.Server
	TestServer *httptest.Server
	DB         *database.DB
	// Programs is the stand-in for the Programs service, whose answers a test
	// sets.
	Programs *fakePrograms
	// OpenContent is the stand-in for Open Content's service, whose answers a
	// test sets.
	OpenContent *fakeOpenContent
	Client      *Client
	Context     context.Context
	Cancel      context.CancelFunc
	T           *testing.T
}

func SetupTestEnv(t *testing.T) *TestEnv {
	ctx, cancel := context.WithCancel(context.Background())

	providers := startFakeProviderPlatforms(t)
	openContent := startFakeOpenContent(t)
	programs := startFakePrograms(t)
	server := handlers.NewServer(true, ctx)
	if server == nil {
		t.Fatal("Failed to create test server")
	}
	providers.attach(t, server.Db.DB)

	//SeedTestData(server.Db)
	server.RegisterRoutes()

	ts := httptest.NewServer(server.Handler())

	client := NewClient(ts.URL)

	return &TestEnv{
		Server:      server,
		TestServer:  ts,
		DB:          server.Db,
		Programs:    programs,
		OpenContent: openContent,
		Client:      client,
		Context:     ctx,
		Cancel:      cancel,
		T:           t,
	}
}

func (env *TestEnv) CleanupTestEnv() {
	if env.TestServer != nil {
		env.TestServer.Close()
	}

	if env.Cancel != nil {
		env.Cancel()
	}

	if env.DB == nil || env.DB.DB == nil {
		return
	}

	sqlDb, err := env.DB.DB.DB()
	if err != nil {
		env.T.Errorf("failed to get sql database during cleanup: %v", err)
	}

	if err := sqlDb.Close(); err != nil {
		env.T.Errorf("failed to close sql database during cleanup: %v", err)
	}
}

func (env *TestEnv) WaitForServer(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", env.TestServer.Listener.Addr().String(), time.Second)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				env.T.Logf("error closing connection: %v", closeErr)
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return &net.OpError{
		Op:   "dial",
		Net:  "tcp",
		Addr: nil,
		Err:  &timeoutError{},
	}
}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout waiting for server to be ready" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

func (env *TestEnv) CreateTestUser(username string, role models.UserRole, facilityId uint, residentId string) (*models.User, error) {
	user := &models.User{
		Username:   username,
		NameFirst:  "Test",
		NameLast:   "User",
		Role:       role,
		Email:      username + "@example.com",
		DocID:      models.NilIfBlank(residentId),
		FacilityID: facilityId,
	}

	if err := env.DB.CreateUser(user); err != nil {
		return nil, err
	}

	return user, nil
}

func (env *TestEnv) CreateTestFacility(name string) (*models.Facility, error) {

	facility := &models.Facility{
		Timezone: "America/Chicago",
		Name:     name,
	}

	if err := env.DB.CreateFacility(facility); err != nil {
		return nil, err
	}

	return facility, nil
}
