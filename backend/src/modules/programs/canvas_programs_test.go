package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"github.com/stretchr/testify/assert"
)

// These tests pin down the behavior of the live provider path before it is
// generalized to support a second provider. The synthetic ID arithmetic is the
// part that guarantees two providers never collide, so it is covered first.

func TestEncodeDecodeCanvasClassID(t *testing.T) {
	tests := []struct {
		name        string
		providerID  uint
		rawCourseID uint
	}{
		{"first provider, first course", 1, 1},
		{"provider zero", 0, 42},
		{"max course id", 1, 999_999},
		{"max provider id", 999, 999_999},
		{"typical canvas course", 2, 105},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeCanvasClassID(tt.providerID, tt.rawCourseID)
			assert.GreaterOrEqual(t, encoded, CanvasClassIDOffset,
				"encoded id must clear the offset so the dispatch guards fire")
			provider, course := decodeCanvasClassID(encoded)
			assert.Equal(t, tt.providerID, provider)
			assert.Equal(t, tt.rawCourseID, course)
		})
	}
}

// Two providers must never produce the same class ID for different courses.
// This is what lets a second provider reuse the existing offsets untouched.
func TestCanvasClassIDsDoNotCollideAcrossProviders(t *testing.T) {
	seen := map[uint]string{}
	for providerID := uint(1); providerID <= 3; providerID++ {
		for _, rawCourseID := range []uint{1, 2, 105, 999_999} {
			id := encodeCanvasClassID(providerID, rawCourseID)
			key := fmt.Sprintf("provider=%d course=%d", providerID, rawCourseID)
			if prev, dup := seen[id]; dup {
				t.Fatalf("class id %d produced by both %q and %q", id, prev, key)
			}
			seen[id] = key
		}
	}
}

func TestEncodeDecodeFacilityCanvasClassID(t *testing.T) {
	tests := []struct {
		name        string
		facilityID  uint
		providerID  uint
		rawCourseID uint
	}{
		{"typical", 1, 1, 105},
		{"zero facility", 0, 2, 7},
		{"max values", 999, 999, 999_999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeFacilityCanvasClassID(tt.facilityID, tt.providerID, tt.rawCourseID)
			assert.GreaterOrEqual(t, encoded, CanvasFacilityClassIDOffset)
			assert.GreaterOrEqual(t, encoded, CanvasClassIDOffset,
				"facility-scoped ids must still satisfy the plain class guards")
			facility, provider, course := decodeFacilityCanvasClassID(encoded)
			assert.Equal(t, tt.facilityID, facility)
			assert.Equal(t, tt.providerID, provider)
			assert.Equal(t, tt.rawCourseID, course)
		})
	}
}

func TestResolveCanvasClassParts(t *testing.T) {
	t.Run("facility-scoped id returns embedded facility", func(t *testing.T) {
		encoded := encodeFacilityCanvasClassID(3, 2, 105)
		facility, provider, course := resolveCanvasClassParts(encoded)
		assert.Equal(t, uint(3), facility)
		assert.Equal(t, uint(2), provider)
		assert.Equal(t, uint(105), course)
	})

	t.Run("old-style id reports facility 0 so caller falls back to claims", func(t *testing.T) {
		encoded := encodeCanvasClassID(2, 105)
		facility, provider, course := resolveCanvasClassParts(encoded)
		assert.Equal(t, uint(0), facility)
		assert.Equal(t, uint(2), provider)
		assert.Equal(t, uint(105), course)
	})
}

func TestIsCanvasProvider(t *testing.T) {
	tests := []struct {
		providerType ppapi.ProviderType
		want         bool
	}{
		{ppapi.CanvasOSS, true},
		{ppapi.CanvasCloud, true},
		{ppapi.Kolibri, false},
		{ppapi.Brightspace, false},
		{ppapi.ProviderType("essential_ed"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.providerType), func(t *testing.T) {
			got := isCanvasProvider(&ppapi.Provider{Type: tt.providerType})
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseCanvasCourse(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	t.Run("full course", func(t *testing.T) {
		entry, ok := parseCanvasCourse(map[string]interface{}{
			"id":          float64(105),
			"name":        "Introduction to Botany",
			"course_code": "BOT101",
			"start_at":    "2026-01-05T00:00:00Z",
			"end_at":      "2026-12-05T00:00:00Z",
		}, 2, now)

		assert.True(t, ok)
		assert.Equal(t, uint(105), entry.rawID)
		assert.Equal(t, encodeCanvasClassID(2, 105), entry.encodedID)
		assert.Equal(t, "Introduction to Botany", entry.name)
		assert.Equal(t, "BOT101", entry.description)
		assert.Equal(t, 2026, entry.startDt.Year())
		assert.NotNil(t, entry.endDt)
		assert.Equal(t, Active, entry.status, "end date in the future stays Active")
	})

	t.Run("past end date marks the course Completed", func(t *testing.T) {
		entry, ok := parseCanvasCourse(map[string]interface{}{
			"id":     float64(7),
			"name":   "Old Course",
			"end_at": "2020-01-01T00:00:00Z",
		}, 1, now)

		assert.True(t, ok)
		assert.Equal(t, Completed, entry.status)
	})

	t.Run("missing end date stays Active with nil endDt", func(t *testing.T) {
		entry, ok := parseCanvasCourse(map[string]interface{}{
			"id":   float64(7),
			"name": "Ongoing",
		}, 1, now)

		assert.True(t, ok)
		assert.Nil(t, entry.endDt)
		assert.Equal(t, Active, entry.status)
	})

	t.Run("non-numeric id is rejected rather than panicking", func(t *testing.T) {
		_, ok := parseCanvasCourse(map[string]interface{}{"id": "105"}, 1, now)
		assert.False(t, ok)
	})

	t.Run("missing id is rejected", func(t *testing.T) {
		_, ok := parseCanvasCourse(map[string]interface{}{"name": "No ID"}, 1, now)
		assert.False(t, ok)
	})

	t.Run("unparseable dates do not fail the course", func(t *testing.T) {
		entry, ok := parseCanvasCourse(map[string]interface{}{
			"id":       float64(9),
			"start_at": "not-a-date",
			"end_at":   "also-not-a-date",
		}, 1, now)

		assert.True(t, ok)
		assert.True(t, entry.startDt.IsZero())
		assert.Nil(t, entry.endDt)
	})
}

func TestBuildCourseTimezoneMap(t *testing.T) {
	got := buildCourseTimezoneMap([]map[string]interface{}{
		{"id": float64(1), "time_zone": "America/Chicago"},
		{"id": float64(2), "time_zone": ""},
		{"id": float64(3)},
		{"name": "no id"},
		{"id": float64(4), "time_zone": "America/New_York"},
	})

	assert.Equal(t, map[uint]string{
		1: "America/Chicago",
		4: "America/New_York",
	}, got, "courses without a usable time_zone are omitted entirely")
}

// fetchAllCanvasPages is the function that gains a pagination seam when a second
// provider is added, so its current contract is pinned here: bearer auth, Link
// header paging, accumulation across pages, and a hard error on non-200.
func TestFetchAllCanvasPages(t *testing.T) {
	t.Run("sends bearer auth and accepts json", func(t *testing.T) {
		var gotAuth, gotAccept string
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotAccept = r.Header.Get("Accept")
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": float64(1)}})
		}))
		defer ts.Close()

		srv := &server{Client: ts.Client()}
		provider := &ppapi.Provider{AccessKey: "secret-token"}

		out, err := srv.fetchAllCanvasPages(context.Background(), provider, ts.URL, 0)
		assert.NoError(t, err)
		assert.Len(t, out, 1)
		assert.Equal(t, "Bearer secret-token", gotAuth)
		assert.Equal(t, "application/json", gotAccept)
	})

	t.Run("follows Link rel=next across pages", func(t *testing.T) {
		var ts *httptest.Server
		calls := 0
		ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Link", `<`+ts.URL+`/page2>; rel="next"`)
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": float64(1)}})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": float64(2)}})
		}))
		defer ts.Close()

		srv := &server{Client: ts.Client()}
		out, err := srv.fetchAllCanvasPages(context.Background(), &ppapi.Provider{}, ts.URL, 0)

		assert.NoError(t, err)
		assert.Equal(t, 2, calls)
		assert.Len(t, out, 2, "results from every page are accumulated")
	})

	t.Run("non-200 is an error, not an empty result", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer ts.Close()

		srv := &server{Client: ts.Client()}
		out, err := srv.fetchAllCanvasPages(context.Background(), &ppapi.Provider{}, ts.URL, 0)

		assert.Error(t, err)
		assert.Nil(t, out)
	})

	t.Run("malformed json is an error", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"not":"an array"}`))
		}))
		defer ts.Close()

		srv := &server{Client: ts.Client()}
		_, err := srv.fetchAllCanvasPages(context.Background(), &ppapi.Provider{}, ts.URL, 0)

		assert.Error(t, err)
	})

	t.Run("a cancelled context stops the fetch", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		}))
		defer ts.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		srv := &server{Client: ts.Client()}
		_, err := srv.fetchAllCanvasPages(ctx, &ppapi.Provider{}, ts.URL, 0)

		assert.Error(t, err)
	})
}

// --- the live-provider seam ---------------------------------------------------

func TestIsLiveProgramProvider(t *testing.T) {
	tests := []struct {
		providerType ppapi.ProviderType
		want         bool
	}{
		{ppapi.CanvasOSS, true},
		{ppapi.CanvasCloud, true},
		{ppapi.Kolibri, false},
		{ppapi.Brightspace, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.providerType), func(t *testing.T) {
			got := isLiveProgramProvider(&ppapi.Provider{Type: tt.providerType})
			assert.Equal(t, tt.want, got)
		})
	}
}

// A provider's access key rides on every request to its base URL, so a plaintext
// URL would put it on the wire. The handlers reject one on the way in; the
// transports refuse to send to one regardless of how the URL got there.
func TestRequireHTTPS(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{"an https url is accepted", "https://api.essentialed.com/v20181129", false},
		{"surrounding whitespace is tolerated", "  https://canvas.example.com  ", false},
		{"plaintext http is refused", "http://canvas.example.com", true},
		{"a scheme-less url is refused", "canvas.example.com", true},
		{"an empty url is refused", "", true},
		{"another scheme is refused", "ftp://canvas.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireHTTPS(tt.rawURL)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// The guard has to hold on every page, not just the first: a Canvas Link header
// names the next page, and that URL comes from the provider.
func TestFetchAllCanvasPagesRefusesPlaintextNextPage(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<http://canvas.example.com/api/v1/courses?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[{"id":1}]`))
	}))
	defer ts.Close()
	srv := &server{Client: ts.Client()}
	provider := &ppapi.Provider{BaseURL: ts.URL, AccessKey: "secret-token", Type: ppapi.CanvasOSS}

	out, err := srv.fetchAllCanvasPages(context.Background(), provider, ts.URL+"/api/v1/courses", 0)

	assert.Error(t, err)
	assert.Nil(t, out)
}

func TestTransportFor(t *testing.T) {
	srv := &server{}

	t.Run("canvas types get the canvas transport", func(t *testing.T) {
		for _, pt := range []ppapi.ProviderType{ppapi.CanvasOSS, ppapi.CanvasCloud} {
			transport, err := srv.transportFor(&ppapi.Provider{Type: pt})
			assert.NoError(t, err)
			assert.IsType(t, canvasTransport{}, transport)
		}
	})

	t.Run("non-live types are rejected", func(t *testing.T) {
		for _, pt := range []ppapi.ProviderType{ppapi.Kolibri, ppapi.Brightspace} {
			transport, err := srv.transportFor(&ppapi.Provider{Type: pt})
			assert.Error(t, err)
			assert.Nil(t, transport)
		}
	})
}

func TestCanvasURLBuilders(t *testing.T) {
	provider := &ppapi.Provider{BaseURL: "http://canvas", AccountID: "1"}

	assert.Equal(t,
		"http://canvas/api/v1/accounts/1/courses?per_page=100",
		canvasAccountCoursesURL(provider))

	assert.Equal(t,
		"http://canvas/api/v1/courses/105",
		canvasCourseURL(provider, 105))
}

func TestCanvasTransportFetchOne(t *testing.T) {
	t.Run("returns the decoded record and authorizes the request", func(t *testing.T) {
		var gotAuth string
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": float64(105), "time_zone": "America/Chicago",
			})
		}))
		defer ts.Close()

		transport := canvasTransport{srv: &server{Client: ts.Client()}}
		provider := &ppapi.Provider{AccessKey: "tok"}

		record, err := transport.fetchOne(context.Background(), provider, ts.URL)
		assert.NoError(t, err)
		assert.Equal(t, "Bearer tok", gotAuth)
		assert.Equal(t, "America/Chicago", record["time_zone"])
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		transport := canvasTransport{srv: &server{Client: ts.Client()}}
		record, err := transport.fetchOne(context.Background(), &ppapi.Provider{}, ts.URL)
		assert.Error(t, err)
		assert.Nil(t, record)
	})

	t.Run("malformed json is an error", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[not an object]`))
		}))
		defer ts.Close()

		transport := canvasTransport{srv: &server{Client: ts.Client()}}
		_, err := transport.fetchOne(context.Background(), &ppapi.Provider{}, ts.URL)
		assert.Error(t, err)
	})
}

func TestCanvasProviderListCourses(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0).Format("2006-01-02T15:04:05Z")
	past := time.Now().AddDate(-1, 0, 0).Format("2006-01-02T15:04:05Z")

	newReader := func(t *testing.T, payload []map[string]interface{}) (LiveProgramProvider, func()) {
		t.Helper()
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(payload)
		}))
		srv := &server{Client: ts.Client()}
		provider := &ppapi.Provider{BaseURL: ts.URL, AccountID: "1", Type: ppapi.CanvasOSS}
		reader, err := srv.newLiveProgramProvider(provider)
		assert.NoError(t, err)
		return reader, ts.Close
	}

	t.Run("parses courses and collects timezones", func(t *testing.T) {
		reader, done := newReader(t, []map[string]interface{}{
			{"id": float64(1), "name": "Active", "end_at": future, "time_zone": "America/Chicago"},
			{"id": float64(2), "name": "Done", "end_at": past},
		})
		defer done()

		listing, err := reader.ListCourses(context.Background())
		assert.NoError(t, err)
		assert.Len(t, listing.Courses, 2)
		assert.Equal(t, 2, listing.Total)
		assert.Equal(t, Active, listing.Courses[0].status)
		assert.Equal(t, Completed, listing.Courses[1].status)
		assert.Equal(t, map[uint]string{1: "America/Chicago"}, listing.Timezones)
	})

	// Total counts what the provider returned; Courses counts what we could parse.
	// The overview's class count is reported against Total, so this gap matters.
	t.Run("unparseable records count toward Total but are not returned", func(t *testing.T) {
		reader, done := newReader(t, []map[string]interface{}{
			{"id": float64(1), "name": "Good"},
			{"id": "not-a-number", "name": "Bad"},
		})
		defer done()

		listing, err := reader.ListCourses(context.Background())
		assert.NoError(t, err)
		assert.Len(t, listing.Courses, 1)
		assert.Equal(t, 2, listing.Total)
	})

	t.Run("empty listing is not an error", func(t *testing.T) {
		reader, done := newReader(t, []map[string]interface{}{})
		defer done()

		listing, err := reader.ListCourses(context.Background())
		assert.NoError(t, err)
		assert.Empty(t, listing.Courses)
		assert.Equal(t, 0, listing.Total)
	})

	t.Run("encoded ids are scoped to the provider", func(t *testing.T) {
		reader, done := newReader(t, []map[string]interface{}{{"id": float64(105), "name": "C"}})
		defer done()

		listing, err := reader.ListCourses(context.Background())
		assert.NoError(t, err)
		// The test provider has a zero ID, so this pins the encoding, not the value.
		assert.Equal(t, encodeCanvasClassID(0, 105), listing.Courses[0].encodedID)
	})

	t.Run("a transport error propagates", func(t *testing.T) {
		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer ts.Close()

		srv := &server{Client: ts.Client()}
		reader, err := srv.newLiveProgramProvider(
			&ppapi.Provider{BaseURL: ts.URL, AccountID: "1", Type: ppapi.CanvasOSS})
		assert.NoError(t, err)

		_, err = reader.ListCourses(context.Background())
		assert.Error(t, err)
	})
}

func TestNewLiveProgramProviderRejectsNonLiveTypes(t *testing.T) {
	srv := &server{}
	for _, pt := range []ppapi.ProviderType{ppapi.Kolibri, ppapi.Brightspace} {
		reader, err := srv.newLiveProgramProvider(&ppapi.Provider{Type: pt})
		assert.Error(t, err)
		assert.Nil(t, reader)
	}
}

// filterCanvasPrograms and its helpers close the gap from the #1232 review:
// Canvas rows have no backing `programs` row, so GetProgramsOverviewTable's
// SQL WHERE clauses never see them, and before this they were appended to
// every response unfiltered and counted in meta.total regardless of what the
// admin was actually filtering for.

func canvasRow(id uint, name string, totalActiveClasses, totalActiveEnrollments *int64, types string, loading bool) ProgramsOverviewTable {
	return ProgramsOverviewTable{
		ProgramID:              id,
		ProgramName:            name,
		Description:            "Courses pulled live from a connection: " + name,
		TotalActiveClasses:     totalActiveClasses,
		TotalActiveEnrollments: totalActiveEnrollments,
		Types:                  types,
		Status:                 true,
		Source:                 "canvas",
		Loading:                loading,
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestCanvasStatusMatches(t *testing.T) {
	assert.True(t, canvasStatusMatches("Available"))
	assert.True(t, canvasStatusMatches("Inactive|Available"))
	assert.False(t, canvasStatusMatches("Inactive"))
	assert.False(t, canvasStatusMatches("Archived"))
	assert.False(t, canvasStatusMatches("Inactive|Archived"))
}

func TestCanvasTypesMatch(t *testing.T) {
	assert.True(t, canvasTypesMatch("College", "College"))
	assert.True(t, canvasTypesMatch("College", "Employment|College"))
	assert.True(t, canvasTypesMatch("College", "college")) // SQL side is case-insensitive (~*)
	assert.False(t, canvasTypesMatch("College", "Employment"))
}

func TestCanvasNumericMatches(t *testing.T) {
	tests := []struct {
		name     string
		value    *int64
		filter   string
		expected bool
	}{
		{"matches >=", int64Ptr(5), ">=5", true},
		{"fails >=", int64Ptr(3), ">=5", false},
		{"matches exact", int64Ptr(5), "=5", true},
		{"malformed value does not exclude the row", int64Ptr(5), "garbage", true},
		{"nil metric (still loading) cannot satisfy a real filter", nil, ">=0", false},
		{"nil metric with malformed filter still passes (filter is a no-op)", nil, "garbage", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, canvasNumericMatches(tt.value, tt.filter))
		})
	}
}

func TestFilterCanvasPrograms(t *testing.T) {
	rows := []ProgramsOverviewTable{
		canvasRow(1, "Canvas - DOC", int64Ptr(4), int64Ptr(10), "College", false),
		canvasRow(2, "Canvas - Facility B", int64Ptr(1), int64Ptr(2), "College", false),
		canvasRow(3, "Canvas - Loading", nil, nil, "", true),
	}

	t.Run("no filters keeps every row", func(t *testing.T) {
		out := filterCanvasPrograms(rows, &QueryContext{}, map[string]string{})
		assert.Len(t, out, 3)
	})

	t.Run("search matches provider name", func(t *testing.T) {
		out := filterCanvasPrograms(rows, &QueryContext{Search: "doc"}, map[string]string{})
		assert.Len(t, out, 1)
		assert.Equal(t, uint(1), out[0].ProgramID)
	})

	t.Run("status filter for Available keeps all live canvas rows", func(t *testing.T) {
		out := filterCanvasPrograms(rows, &QueryContext{}, map[string]string{"programs.is_active": "Available"})
		assert.Len(t, out, 3)
	})

	t.Run("status filter for Inactive/Archived excludes every canvas row", func(t *testing.T) {
		out := filterCanvasPrograms(rows, &QueryContext{}, map[string]string{"programs.is_active": "Inactive"})
		assert.Empty(t, out)
	})

	t.Run("numeric filter on total_active_classes excludes rows below threshold and the still-loading row", func(t *testing.T) {
		out := filterCanvasPrograms(rows, &QueryContext{}, map[string]string{"mr.total_active_classes": ">=2"})
		assert.Len(t, out, 1)
		assert.Equal(t, uint(1), out[0].ProgramID)
	})

	t.Run("a filter dimension Canvas rows cannot satisfy excludes every row", func(t *testing.T) {
		for _, col := range []string{"pct.credit_types", "programs.funding_type", "capacity_utilization",
			"completion_rate", "attendance_rate", "mr.total_active_facilities", "facility_id"} {
			out := filterCanvasPrograms(rows, &QueryContext{}, map[string]string{col: "anything"})
			assert.Empty(t, out, "filter %q should exclude all canvas rows", col)
		}
	})

	t.Run("program type filter matches the hardcoded College type", func(t *testing.T) {
		out := filterCanvasPrograms(rows, &QueryContext{}, map[string]string{"pt.program_types": "College"})
		assert.Len(t, out, 2) // the still-loading row has no type yet and is excluded
	})
}

// Canvas rows are appended after the database rows and counted in meta.total,
// so the page window arithmetic has to hold for every page or total/last_page
// disagree between pages and page 1 overflows per_page.
func TestCanvasPageSlice(t *testing.T) {
	canvasRows := func(n int) []ProgramsOverviewTable {
		rows := make([]ProgramsOverviewTable, n)
		for i := range rows {
			rows[i] = ProgramsOverviewTable{ProgramID: CanvasProgramIDOffset + uint(i)}
		}
		return rows
	}
	tests := []struct {
		name          string
		canvasCount   int
		dbTotal       int64
		page, perPage int
		all           bool
		wantIDs       []uint
	}{
		{"db total is an exact multiple of per_page, canvas spills to page 2", 1, 10, 1, 10, false, nil},
		{"spilled canvas row lands on page 2", 1, 10, 2, 10, false, []uint{CanvasProgramIDOffset}},
		{"partial last page has room for the canvas row", 1, 5, 1, 10, false, []uint{CanvasProgramIDOffset}},
		{"canvas rows split across the page boundary", 3, 12, 2, 10, false, []uint{CanvasProgramIDOffset, CanvasProgramIDOffset + 1, CanvasProgramIDOffset + 2}},
		{"page before any canvas position", 3, 25, 1, 10, false, nil},
		{"page straddling the first canvas row", 3, 25, 3, 10, false, []uint{CanvasProgramIDOffset, CanvasProgramIDOffset + 1, CanvasProgramIDOffset + 2}},
		{"page past every canvas row", 2, 5, 3, 5, false, nil},
		{"no canvas rows", 0, 10, 1, 10, false, nil},
		{"all skips pagination", 2, 10, 1, 10, true, []uint{CanvasProgramIDOffset, CanvasProgramIDOffset + 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := QueryContext{Page: tt.page, PerPage: tt.perPage, All: tt.all, Total: tt.dbTotal}
			got := canvasPageSlice(canvasRows(tt.canvasCount), tt.dbTotal, &args)
			gotIDs := make([]uint, 0, len(got))
			for _, p := range got {
				gotIDs = append(gotIDs, p.ProgramID)
			}
			assert.Equal(t, tt.wantIDs, nilIfEmpty(gotIDs))
			if !tt.all {
				assert.LessOrEqual(t, int64(len(got)), max(int64(tt.perPage)-(tt.dbTotal-int64(args.CalcOffset())), 0),
					"page must not return more rows than per_page once the database rows are counted")
			}
		})
	}
}

func nilIfEmpty(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// Canvas rows must be counted in meta.total on every page, otherwise total and
// last_page change depending on which page the client asks for.
func TestCanvasTotalIsPageIndependent(t *testing.T) {
	const dbTotal, canvasCount, perPage = 10, 1, 10
	var totals, lastPages []int64
	for page := 1; page <= 2; page++ {
		args := QueryContext{Page: page, PerPage: perPage, Total: dbTotal}
		args.Total += canvasCount
		meta := args.IntoMeta()
		totals = append(totals, meta.Total)
		lastPages = append(lastPages, int64(meta.LastPage))
	}
	assert.Equal(t, []int64{11, 11}, totals)
	assert.Equal(t, []int64{2, 2}, lastPages)
}

func TestNextPageURL(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "standard next link",
			header: `<https://canvas.example.com/api/v1/courses?page=2>; rel="next"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "next among multiple relations",
			header: `<https://canvas.example.com/api/v1/courses?page=1>; rel="prev", <https://canvas.example.com/api/v1/courses?page=3>; rel="next", <https://canvas.example.com/api/v1/courses?page=10>; rel="last"`,
			want:   "https://canvas.example.com/api/v1/courses?page=3",
		},
		{
			name:   "no next relation",
			header: `<https://canvas.example.com/api/v1/courses?page=10>; rel="last"`,
			want:   "",
		},
		{
			name:   "empty header",
			header: "",
			want:   "",
		},
		{
			name:   "case-insensitive rel name",
			header: `<https://canvas.example.com/api/v1/courses?page=2>; REL="next"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "case-insensitive rel value",
			header: `<https://canvas.example.com/api/v1/courses?page=2>; rel="Next"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "extra whitespace around semicolon and equals",
			header: `<https://canvas.example.com/api/v1/courses?page=2> ;  rel = "next"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "rel before additional params",
			header: `<https://canvas.example.com/api/v1/courses?page=2>; rel="next"; title="Next Page"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "rel after other params (attribute order variation)",
			header: `<https://canvas.example.com/api/v1/courses?page=2>; title="Next Page"; rel="next"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "URL containing a comma",
			header: `<https://canvas.example.com/api/v1/courses?foo=a,b&page=2>; rel="next"`,
			want:   "https://canvas.example.com/api/v1/courses?foo=a,b&page=2",
		},
		{
			name:   "URL containing a semicolon (percent-encoded)",
			header: `<https://canvas.example.com/api/v1/courses?filter=a%3Bb>; rel="next"`,
			want:   "https://canvas.example.com/api/v1/courses?filter=a%3Bb",
		},
		{
			name:   "multiple space-separated rel tokens including next",
			header: `<https://canvas.example.com/api/v1/courses?page=2>; rel="alternate next"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "unquoted rel value",
			header: `<https://canvas.example.com/api/v1/courses?page=2>; rel=next`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
		{
			name:   "first entry has no next, second does",
			header: `<https://canvas.example.com/api/v1/courses?page=1>; rel="current", <https://canvas.example.com/api/v1/courses?page=2>; rel="next"`,
			want:   "https://canvas.example.com/api/v1/courses?page=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextPageURL(tt.header)
			if got != tt.want {
				t.Errorf("NextPageURL(%q)\n  got  %q\n  want %q", tt.header, got, tt.want)
			}
		})
	}
}
