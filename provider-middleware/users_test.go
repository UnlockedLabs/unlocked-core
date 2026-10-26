package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms"

	"github.com/stretchr/testify/assert"
)

// The user lists the module's providerservice.go asks for. The service
// doesn't check the Authorization header: only the server can reach it.
func TestUserRoutes(t *testing.T) {
	db := newTestDB(t)
	sh := newHandler(db)
	sh.Mux = http.NewServeMux()
	sh.registerRoutes()
	fc := newFakeCanvas(t)
	fc.users = []map[string]any{
		canvasUser(11, "Ada Lovelace", "Lovelace, Ada", "Ada", "ada@example.com"),
		canvasUser(15, "Mapped User", "User, Mapped", "Mapped", "mapped@example.com"),
	}
	canvas := createCanvasProvider(t, db, fc)
	createResident(t, db, canvas.ID, 1, "student", "15")
	moodle := createProvider(t, db, providerplatforms.ProviderPlatform{Type: "moodle", Name: "m"})
	down := createProvider(t, db, providerplatforms.ProviderPlatform{Type: providerplatforms.CanvasOSS, Name: "d", BaseUrl: "http://127.0.0.1:1"})
	id := func(p *providerplatforms.ProviderPlatform) string { return strconv.Itoa(int(p.ID)) }

	const (
		ada    = `{"username":"LovelaceAda","name_first":"Ada","name_last":"Lovelace","email":"ada@example.com","external_user_id":"11","external_username":"ada@example.com"}`
		mapped = `{"username":"UserMapped","name_first":"Mapped","name_last":"User","email":"mapped@example.com","external_user_id":"15","external_username":"mapped@example.com"}`
	)
	tests := []struct {
		name     string
		path     string
		wantCode int
		wantJSON string
		wantBody string
	}{
		{"health", "/", http.StatusOK, "", ""},
		{"unmapped users", "/api/users?id=" + id(canvas), http.StatusOK, "[" + ada + "]", ""},
		{"all users", "/api/users/all?id=" + id(canvas), http.StatusOK, "[" + ada + "," + mapped + "]", ""},
		{"bad id", "/api/users?id=abc", http.StatusBadRequest, "", "Failed to initialize provider service\n"},
		{"no such provider", "/api/users?id=999", http.StatusNotFound, "", "Failed to initialize provider service\n"},
		{"unsupported provider", "/api/users?id=" + id(moodle), http.StatusBadRequest, "", "Failed to initialize provider service\n"},
		{"provider down", "/api/users?id=" + id(down), http.StatusInternalServerError, "", "Failed to retrieve users\n"},
		{"all: bad id", "/api/users/all?id=abc", http.StatusBadRequest, "", "Failed to initialize service\n"},
		{"all: no such provider", "/api/users/all?id=999", http.StatusNotFound, "", "Failed to initialize service\n"},
		{"all: unsupported provider", "/api/users/all?id=" + id(moodle), http.StatusBadRequest, "", "Failed to initialize service\n"},
		{"all: provider down", "/api/users/all?id=" + id(down), http.StatusInternalServerError, "", "Failed to retrieve users\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			sh.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			assert.Equal(t, tt.wantCode, rec.Code)
			if tt.wantJSON != "" {
				assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
				assert.JSONEq(t, tt.wantJSON, rec.Body.String())
				return
			}
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}
