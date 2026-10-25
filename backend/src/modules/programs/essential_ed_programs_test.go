package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The payloads here mirror what the real sandbox returns, including the quirks
// the published docs get wrong: classes report active as 1/0 while users report
// it as a JSON boolean, close_date arrives as null, and datetimes are space
// separated rather than RFC3339.

func essentialEdServer(t *testing.T, handler http.HandlerFunc) (*server, *ppapi.Provider, func()) {
	t.Helper()
	ts := httptest.NewTLSServer(handler)
	srv := &server{Client: ts.Client()}
	provider := &ppapi.Provider{
		BaseURL:   ts.URL,
		AccountID: "303",
		AccessKey: "test-token",
		Type:      ppapi.EssentialEd,
	}
	return srv, provider, ts.Close
}

func envelope(records []map[string]interface{}, atEnd bool) map[string]interface{} {
	return map[string]interface{}{"records": records, "at_end": atEnd}
}

func TestEssentialEdTransportAuthorize(t *testing.T) {
	var gotToken, gotAgent, gotAccept string
	srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-API-Token")
		gotAgent = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		_ = json.NewEncoder(w).Encode(envelope(nil, true))
	})
	defer done()

	transport := essentialEdTransport{srv: srv}
	_, err := transport.fetchAll(context.Background(), provider, provider.BaseURL+"/classes", 0)

	assert.NoError(t, err)
	assert.Equal(t, "test-token", gotToken)
	assert.Equal(t, "application/json", gotAccept)
	// Required: the API answers with an HTML error page when it is missing.
	assert.NotEmpty(t, gotAgent, "User-Agent must always be sent")
}

func TestEssentialEdTransportFetchAll(t *testing.T) {
	t.Run("walks start/limit pages until at_end", func(t *testing.T) {
		var starts []string
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			starts = append(starts, r.URL.Query().Get("start"))
			assert.Equal(t, "100", r.URL.Query().Get("limit"))
			if len(starts) == 1 {
				page := make([]map[string]interface{}, 100)
				for i := range page {
					page[i] = map[string]interface{}{"id": float64(i + 1)}
				}
				_ = json.NewEncoder(w).Encode(envelope(page, false))
				return
			}
			_ = json.NewEncoder(w).Encode(envelope(
				[]map[string]interface{}{{"id": float64(101)}}, true))
		})
		defer done()

		transport := essentialEdTransport{srv: srv}
		out, err := transport.fetchAll(context.Background(), provider, provider.BaseURL+"/classes", 0)

		assert.NoError(t, err)
		assert.Len(t, out, 101)
		assert.Equal(t, []string{"0", "100"}, starts, "start advances by the number already collected")
	})

	t.Run("preserves query params already on the url", func(t *testing.T) {
		var gotSchool string
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotSchool = r.URL.Query().Get("school_id")
			_ = json.NewEncoder(w).Encode(envelope(nil, true))
		})
		defer done()

		transport := essentialEdTransport{srv: srv}
		_, err := transport.fetchAll(context.Background(), provider, provider.BaseURL+"/classes?school_id=6592", 0)

		assert.NoError(t, err)
		assert.Equal(t, "6592", gotSchool)
	})

	// at_end is the normal terminator; an empty page must also stop the loop so a
	// server that never sets at_end cannot spin to the page cap.
	t.Run("an empty page terminates even when at_end stays false", func(t *testing.T) {
		calls := 0
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			_ = json.NewEncoder(w).Encode(envelope([]map[string]interface{}{}, false))
		})
		defer done()

		transport := essentialEdTransport{srv: srv}
		out, err := transport.fetchAll(context.Background(), provider, provider.BaseURL+"/classes", 0)

		assert.NoError(t, err)
		assert.Empty(t, out)
		assert.Equal(t, 1, calls)
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		defer done()

		transport := essentialEdTransport{srv: srv}
		_, err := transport.fetchAll(context.Background(), provider, provider.BaseURL+"/classes", 0)
		assert.Error(t, err)
	})

	// Without a User-Agent the real API returns HTML, so a decode failure here
	// must surface as an error rather than an empty result set.
	t.Run("an html body is an error, not an empty listing", func(t *testing.T) {
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<p>Sorry, you don't have access to this resource.</p>"))
		})
		defer done()

		transport := essentialEdTransport{srv: srv}
		_, err := transport.fetchAll(context.Background(), provider, provider.BaseURL+"/classes", 0)
		assert.Error(t, err)
	})
}

func TestParseEssentialEdClass(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	// Verbatim shape of a real sandbox class record.
	sandboxClass := func() map[string]interface{} {
		return map[string]interface{}{
			"id":           float64(69742),
			"active":       float64(1),
			"name":         "College Prep I",
			"open_date":    "2026-01-31 00:00:00",
			"close_date":   nil,
			"schedule_mon": "1pm-2pm",
			"school_id":    float64(6592),
			"owner_id":     float64(4329931),
			"created_at":   "2026-09-15 19:58:59",
			"students": []interface{}{
				map[string]interface{}{"id": float64(4329946), "first_name": "UnlockedLabs", "last_name": "Student1"},
				map[string]interface{}{"id": float64(4329947), "first_name": "UnlockedLabs", "last_name": "Student2"},
			},
		}
	}

	t.Run("maps a real class record", func(t *testing.T) {
		course, ok := parseEssentialEdClass(sandboxClass(), 2, now)

		assert.True(t, ok)
		assert.Equal(t, uint(69742), course.rawID)
		assert.Equal(t, encodeCanvasClassID(2, 69742), course.encodedID)
		assert.Equal(t, "College Prep I", course.name)
		assert.Equal(t, Active, course.status)
		assert.Equal(t, 2026, course.startDt.Year())
		assert.Equal(t, time.January, course.startDt.Month())
		assert.Nil(t, course.endDt, "a null close_date is no end date")
		assert.Equal(t, []string{"4329946", "4329947"}, course.enrolleeIDs)
	})

	t.Run("active=0 marks the class Completed", func(t *testing.T) {
		record := sandboxClass()
		record["active"] = float64(0)
		course, ok := parseEssentialEdClass(record, 2, now)
		assert.True(t, ok)
		assert.Equal(t, Completed, course.status)
	})

	t.Run("a past close_date marks the class Completed", func(t *testing.T) {
		record := sandboxClass()
		record["close_date"] = "2020-06-01 00:00:00"
		course, ok := parseEssentialEdClass(record, 2, now)
		assert.True(t, ok)
		assert.Equal(t, Completed, course.status)
		assert.NotNil(t, course.endDt)
	})

	t.Run("a future close_date stays Active", func(t *testing.T) {
		record := sandboxClass()
		record["close_date"] = "2030-06-01 00:00:00"
		course, ok := parseEssentialEdClass(record, 2, now)
		assert.True(t, ok)
		assert.Equal(t, Active, course.status)
	})

	t.Run("a class with no students gets an empty, non-nil enrollee list", func(t *testing.T) {
		record := sandboxClass()
		record["students"] = []interface{}{}
		course, ok := parseEssentialEdClass(record, 2, now)
		assert.True(t, ok)
		assert.NotNil(t, course.enrolleeIDs, "empty differs from not supplied")
		assert.Empty(t, course.enrolleeIDs)
	})

	t.Run("a missing id is rejected", func(t *testing.T) {
		record := sandboxClass()
		delete(record, "id")
		_, ok := parseEssentialEdClass(record, 2, now)
		assert.False(t, ok)
	})

	// The encoding packs the course into the low six digits. Essential Ed user
	// IDs already exceed that, so class IDs plausibly will too -- encoding one
	// would silently attribute the class to a different provider.
	t.Run("a course id too large to encode is skipped", func(t *testing.T) {
		record := sandboxClass()
		record["id"] = float64(1_000_001)
		_, ok := parseEssentialEdClass(record, 2, now)
		assert.False(t, ok)
	})

	t.Run("a malformed date does not fail the class", func(t *testing.T) {
		record := sandboxClass()
		record["open_date"] = "31/01/2026"
		course, ok := parseEssentialEdClass(record, 2, now)
		assert.True(t, ok)
		assert.True(t, course.startDt.IsZero())
	})
}

func TestEssentialEdBool(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  bool
		known bool
	}{
		{"integer one, as classes report it", float64(1), true, true},
		{"integer zero", float64(0), false, true},
		{"json true, as users report it", true, true, true},
		{"json false", false, false, true},
		{"string one", "1", true, true},
		{"string yes", "yes", true, true},
		{"string false", "false", false, true},
		{"absent", nil, false, false},
		{"unrecognized string", "maybe", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := essentialEdBool(tt.value)
			assert.Equal(t, tt.known, known)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEssentialEdTime(t *testing.T) {
	t.Run("parses the space separated utc format", func(t *testing.T) {
		got := essentialEdTime("2026-01-31 13:45:09")
		assert.Equal(t, 2026, got.Year())
		assert.Equal(t, 13, got.Hour())
		assert.Equal(t, time.UTC, got.Location())
	})

	for _, bad := range []any{nil, "", "2026-01-31T13:45:09Z", float64(12345), "not a date"} {
		t.Run(fmt.Sprintf("zero for %v", bad), func(t *testing.T) {
			assert.True(t, essentialEdTime(bad).IsZero())
		})
	}

	t.Run("pointer variant is nil when there is no value", func(t *testing.T) {
		assert.Nil(t, essentialEdTimePtr(nil))
		assert.NotNil(t, essentialEdTimePtr("2026-01-31 00:00:00"))
	})
}

func TestEssentialEdProviderListCourses(t *testing.T) {
	srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/classes", r.URL.Path)
		_ = json.NewEncoder(w).Encode(envelope([]map[string]interface{}{
			{
				"id": float64(69742), "active": float64(1), "name": "College Prep I",
				"open_date": "2026-01-31 00:00:00", "close_date": nil,
				"students": []interface{}{map[string]interface{}{"id": float64(4329946)}},
			},
			{"id": "bogus", "name": "Unparseable"},
		}, true))
	})
	defer done()

	reader, err := srv.newLiveProgramProvider(provider)
	assert.NoError(t, err)
	assert.IsType(t, essentialEdProvider{}, reader)

	listing, err := reader.ListCourses(context.Background())
	assert.NoError(t, err)
	assert.Len(t, listing.Courses, 1)
	assert.Equal(t, 2, listing.Total, "unparseable records still count toward the total")
	assert.Empty(t, listing.Timezones, "essential ed reports no per-class timezone")

	// Inline students mean no extra request is needed for enrollees.
	ids, err := courseEnrollees(context.Background(), reader, listing.Courses[0])
	assert.NoError(t, err)
	assert.Equal(t, []string{"4329946"}, ids)
}

func TestEssentialEdProviderIsLive(t *testing.T) {
	provider := &ppapi.Provider{Type: ppapi.EssentialEd}
	assert.True(t, isLiveProgramProvider(provider))
	assert.False(t, isCanvasProvider(provider), "essential ed must not be mistaken for canvas")
	assert.Equal(t, "essential_ed", providerSourceLabel(provider))

	srv := &server{}
	transport, err := srv.transportFor(provider)
	assert.NoError(t, err)
	assert.IsType(t, essentialEdTransport{}, transport)
}

func TestCourseIDFitsEncoding(t *testing.T) {
	assert.True(t, courseIDFitsEncoding(0))
	assert.True(t, courseIDFitsEncoding(69742))
	assert.True(t, courseIDFitsEncoding(maxRawCourseID))
	assert.False(t, courseIDFitsEncoding(maxRawCourseID+1))
}

// The enrollee counters used to call the Canvas-specific fetch directly, which
// built a Canvas URL against whatever base_url the provider had. They now go
// through the reader, so a non-Canvas provider resolves its own students.
func TestProviderCourseEnrolleeIDsDispatchesByType(t *testing.T) {
	t.Run("essential ed resolves students from the class listing", func(t *testing.T) {
		var paths []string
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			_ = json.NewEncoder(w).Encode(envelope([]map[string]interface{}{{
				"id": float64(69742), "active": float64(1), "name": "College Prep I",
				"students": []interface{}{
					map[string]interface{}{"id": float64(4329946)},
					map[string]interface{}{"id": float64(4329947)},
				},
			}}, true))
		})
		defer done()

		ids, err := srv.providerCourseEnrolleeIDs(provider, 69742)
		assert.NoError(t, err)
		assert.Equal(t, []string{"4329946", "4329947"}, ids)
		assert.Equal(t, []string{"/classes"}, paths, "no canvas enrollments endpoint is called")
	})

	t.Run("an unknown course id is an error, not a silent empty list", func(t *testing.T) {
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(envelope(nil, true))
		})
		defer done()

		_, err := srv.providerCourseEnrolleeIDs(provider, 999)
		assert.Error(t, err)
	})

	t.Run("a non-live provider is rejected", func(t *testing.T) {
		srv := &server{}
		_, err := srv.providerCourseEnrolleeIDs(&ppapi.Provider{Type: ppapi.Kolibri}, 1)
		assert.Error(t, err)
	})
}

func TestEssentialEdListUsers(t *testing.T) {
	// Real sandbox shapes: students carry a username and no email, staff the
	// reverse, and the field is email_address rather than the documented email.
	t.Run("maps student and staff records", func(t *testing.T) {
		var gotPath, gotQuery string
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("roles[]")
			_ = json.NewEncoder(w).Encode(envelope([]map[string]interface{}{
				{
					"id": float64(4329946), "first_name": "UnlockedLabs", "last_name": "Student1",
					"username": "student1", "email_address": "", "active": true,
				},
				{
					"id": float64(4329931), "first_name": "UnlockedLabs", "last_name": "Teacher1",
					"username": "", "email_address": "teacher1@essentialed.com", "active": true,
				},
			}, true))
		})
		defer done()

		reader, err := srv.newLiveProgramProvider(provider)
		assert.NoError(t, err)
		lister, ok := reader.(liveUserLister)
		assert.True(t, ok, "essential ed must implement liveUserLister")

		users, err := lister.ListUsers(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "/users", gotPath)
		assert.Equal(t, "STUDENT", gotQuery, "roles must be sent as an array parameter")
		assert.Len(t, users, 2)

		assert.Equal(t, "4329946", users[0].ExternalUserID)
		assert.Equal(t, "student1", users[0].Username)
		assert.Equal(t, "UnlockedLabs", users[0].NameFirst)
		assert.Equal(t, "Student1", users[0].NameLast)

		// No username: fall back to the email so the match screen has a handle.
		assert.Equal(t, "teacher1@essentialed.com", users[1].Username)
	})

	t.Run("a record with no id is skipped", func(t *testing.T) {
		srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(envelope([]map[string]interface{}{
				{"first_name": "No", "last_name": "Id"},
			}, true))
		})
		defer done()

		reader, _ := srv.newLiveProgramProvider(provider)
		users, err := reader.(liveUserLister).ListUsers(context.Background())
		assert.NoError(t, err)
		assert.Empty(t, users)
	})
}

// The schedule fields are free-form text typed by instructors, so the parser
// takes the documented format and the spellings people actually use, and skips
// anything it cannot read rather than guessing.
func TestParseMeetingTimes(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []meetingTime
	}{
		{"documented format", "9:00 AM - 11:00 AM", []meetingTime{{540, 660}}},
		{"no spaces around the hyphen", "9:00AM-11:00AM", []meetingTime{{540, 660}}},
		{"24 hour times", "09:00-11:30", []meetingTime{{540, 690}}},
		{"bare hours", "9-11", []meetingTime{{540, 660}}},
		{"two ranges in one day", "9:00 AM - 11:00 AM, 1:00 PM - 2:30 PM", []meetingTime{{540, 660}, {780, 870}}},
		{"en dash", "9:00 AM – 11:00 AM", []meetingTime{{540, 660}}},
		{"the word to", "9:00 am to 11:00 am", []meetingTime{{540, 660}}},
		{"noon and midnight", "12:00 AM - 12:30 PM", []meetingTime{{0, 750}}},
		{"empty", "", nil},
		{"free text with no times", "by appointment", nil},
		{"end before start is dropped", "11:00 AM - 9:00 AM", nil},
		{"a bad range does not lose a good one", "nonsense, 1:00 PM - 2:00 PM", []meetingTime{{780, 840}}},
		{"minutes out of range", "9:75 - 11:00", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseMeetingTimes(tt.text))
		})
	}
}

func TestEssentialEdWeeklySchedule(t *testing.T) {
	t.Run("reads only the days that carry times", func(t *testing.T) {
		schedule := essentialEdWeeklySchedule(map[string]interface{}{
			"schedule_mon": "9:00 AM - 11:00 AM",
			"schedule_wed": "9:00 AM - 11:00 AM",
			"schedule_tue": "",
			"schedule_fri": "by appointment",
		})
		assert.Len(t, schedule, 2)
		assert.Equal(t, []meetingTime{{540, 660}}, schedule[time.Monday])
		assert.Equal(t, []meetingTime{{540, 660}}, schedule[time.Wednesday])
		assert.NotContains(t, schedule, time.Tuesday)
		assert.NotContains(t, schedule, time.Friday, "unparseable text is not an empty meeting")
	})

	t.Run("a class with no schedule reports none", func(t *testing.T) {
		assert.Nil(t, essentialEdWeeklySchedule(map[string]interface{}{"name": "College Prep I"}))
	})
}

// The class detail page reads one class by ID. Essential Ed has no single-class
// endpoint, so it filters the listing -- the students stay inline either way.
func TestEssentialEdGetCourse(t *testing.T) {
	classes := []map[string]interface{}{
		{
			"id": float64(69742), "active": float64(1), "name": "College Prep I",
			"open_date": "2026-01-31 00:00:00", "close_date": nil,
			"students": []interface{}{map[string]interface{}{"id": float64(4329946)}},
		},
		{
			"id": float64(69743), "active": float64(1), "name": "High School Prep I",
			"open_date": "2026-01-31 00:00:00", "close_date": nil,
			"students": []interface{}{map[string]interface{}{"id": float64(4329947)}},
		},
	}

	tests := []struct {
		name        string
		rawCourseID uint
		wantName    string
		wantErr     bool
	}{
		{"returns the requested class", 69743, "High School Prep I", false},
		{"returns the other class", 69742, "College Prep I", false},
		{"an unknown class is an error, not an empty course", 12345, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/classes", r.URL.Path)
				_ = json.NewEncoder(w).Encode(envelope(classes, true))
			})
			defer done()

			reader, err := srv.newLiveProgramProvider(provider)
			assert.NoError(t, err)

			course, timezone, err := reader.GetCourse(context.Background(), tt.rawCourseID)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantName, course.name)
			assert.Equal(t, tt.rawCourseID, course.rawID)
			assert.NotEmpty(t, course.enrolleeIDs, "students come back inline")
			assert.Empty(t, timezone, "essential ed reports no per-class timezone")
		})
	}
}

// A weekly pattern has to become dated events before the month calendar can
// render it, bounded by the class's own open and close dates.
func TestExpandWeeklySchedule(t *testing.T) {
	// March 2026: the 2nd is a Monday, the 4th a Wednesday.
	march := func(day int) time.Time { return time.Date(2026, time.March, day, 0, 0, 0, 0, time.UTC) }
	chicago, err := time.LoadLocation("America/Chicago")
	assert.NoError(t, err)
	course := liveCourse{
		name:    "College Prep I",
		startDt: march(1),
		weeklySchedule: map[time.Weekday][]meetingTime{
			time.Monday:    {{540, 660}},
			time.Wednesday: {{540, 660}, {780, 840}},
		},
	}

	t.Run("expands each meeting across the range", func(t *testing.T) {
		events := expandWeeklySchedule(course, march(2), march(8), time.UTC)
		assert.Len(t, events, 3, "one Monday meeting and two on Wednesday")
		assert.Equal(t, "College Prep I", events[0].Title)
		assert.Equal(t, march(2).Add(9*time.Hour), events[0].StartAt)
		assert.Equal(t, march(2).Add(11*time.Hour), events[0].EndAt)
		assert.Equal(t, march(4).Add(13*time.Hour), events[2].StartAt)
	})

	t.Run("days before the class opens are skipped", func(t *testing.T) {
		late := course
		late.startDt = march(3)
		events := expandWeeklySchedule(late, march(2), march(8), time.UTC)
		assert.Len(t, events, 2, "the Monday before the open date is dropped")
	})

	t.Run("days after the class closes are skipped", func(t *testing.T) {
		closed := course
		end := march(3)
		closed.endDt = &end
		events := expandWeeklySchedule(closed, march(2), march(8), time.UTC)
		assert.Len(t, events, 1, "only the Monday falls inside the close date")
	})

	// The meeting text has no zone, so 9:00 AM has to mean 9:00 AM at the facility
	// rather than 9:00 UTC -- otherwise the calendar renders it hours off.
	t.Run("times are wall-clock in the facility timezone", func(t *testing.T) {
		events := expandWeeklySchedule(course, march(2), march(8), chicago)
		assert.Len(t, events, 3)
		assert.Equal(t, 9, events[0].StartAt.In(chicago).Hour())
		assert.Equal(t, 11, events[0].EndAt.In(chicago).Hour())
		assert.Equal(t, "America/Chicago", events[0].Timezone)
		assert.Equal(t, 15, events[0].StartAt.UTC().Hour(), "9am CST is 15:00 UTC")
	})

	// The calendar hands this function UTC midnights converted into the facility's
	// zone, so each day is 6pm the previous local evening -- an instant already past
	// a close date the provider reported as bare UTC midnight. Comparing instants
	// rather than dates dropped the meetings on the real last day of class.
	t.Run("open and close dates bound the facility's calendar days, not UTC instants", func(t *testing.T) {
		closed := course
		closeDate := march(4) // the class closes on the Wednesday
		closed.endDt = &closeDate
		events := expandWeeklySchedule(closed, march(2).In(chicago), march(8).In(chicago), chicago)
		assert.Len(t, events, 3, "the Monday plus both Wednesday meetings")
		assert.Equal(t, 4, events[2].StartAt.In(chicago).Day(), "the last day of class is kept")

		late := course
		late.startDt = march(3)
		events = expandWeeklySchedule(late, march(2).In(chicago), march(8).In(chicago), chicago)
		assert.Len(t, events, 2, "the Monday before the open date is dropped")
		assert.Equal(t, 4, events[0].StartAt.In(chicago).Day())
	})

	t.Run("a class with no schedule yields no events", func(t *testing.T) {
		assert.Empty(t, expandWeeklySchedule(liveCourse{name: "No Schedule"}, march(2), march(8), time.UTC))
	})
}

// A resident's programs list needs the classes one student is in. Essential Ed
// has no per-user endpoint, so it filters the inline students on each class.
func TestEssentialEdListUserCourses(t *testing.T) {
	classes := []map[string]interface{}{
		{
			"id": float64(69742), "active": float64(1), "name": "College Prep I",
			"open_date": "2026-01-31 00:00:00", "close_date": nil,
			"students": []interface{}{
				map[string]interface{}{"id": float64(4329946)},
				map[string]interface{}{"id": float64(4329947)},
			},
		},
		{
			"id": float64(69743), "active": float64(1), "name": "High School Prep I",
			"open_date": "2026-01-31 00:00:00", "close_date": nil,
			"students": []interface{}{map[string]interface{}{"id": float64(4329946)}},
		},
	}

	tests := []struct {
		name           string
		externalUserID string
		wantNames      []string
	}{
		{"a student in both classes", "4329946", []string{"College Prep I", "High School Prep I"}},
		{"a student in one class", "4329947", []string{"College Prep I"}},
		{"a student in none", "4329948", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(envelope(classes, true))
			})
			defer done()

			reader, err := srv.newLiveProgramProvider(provider)
			assert.NoError(t, err)
			lister, ok := reader.(liveUserCourseLister)
			assert.True(t, ok, "essential ed must report a user's own courses")

			courses, err := lister.ListUserCourses(context.Background(), tt.externalUserID)
			assert.NoError(t, err)
			names := make([]string, 0, len(courses))
			for _, course := range courses {
				names = append(names, course.name)
			}
			assert.Equal(t, tt.wantNames, names)
		})
	}
}

// Canvas keeps its dedicated per-user endpoint rather than filtering a listing.
func TestCanvasDoesNotImplementLiveUserCourseLister(t *testing.T) {
	srv := &server{}
	reader, err := srv.newLiveProgramProvider(&ppapi.Provider{Type: ppapi.CanvasOSS})
	assert.NoError(t, err)
	_, ok := reader.(liveUserCourseLister)
	assert.False(t, ok)
}

// The facility calendar in the main nav reads the same weekly pattern, so a
// provider with no calendar endpoint still shows up beside Canvas classes.
func TestWeeklyScheduleFacilityEvents(t *testing.T) {
	march := func(day int) time.Time { return time.Date(2026, time.March, day, 0, 0, 0, 0, time.UTC) }
	provider := &ppapi.Provider{
		Name: "Essential Education",
		Type: ppapi.EssentialEd,
	}
	provider.ID = 2
	courses := []liveCourse{
		{
			rawID: 69742, name: "College Prep I", startDt: march(1), status: Active,
			weeklySchedule: map[time.Weekday][]meetingTime{time.Monday: {{540, 660}}},
		},
		{rawID: 69743, name: "High School Prep I", startDt: march(1), status: Active},
		{
			// active=0 with no close_date: the pattern would otherwise run forever.
			rawID: 69744, name: "Retired Class", startDt: march(1), status: Completed,
			weeklySchedule: map[time.Weekday][]meetingTime{time.Monday: {{540, 660}}},
		},
	}

	events := weeklyScheduleFacilityEvents(provider, courses, march(2), march(15), time.UTC)

	assert.Len(t, events, 2, "two Mondays in the range; no events for the class with no schedule, none for the completed one")
	assert.Equal(t, "College Prep I", events[0].ClassName)
	assert.Equal(t, Active, events[0].ClassStatus, "the course's own status, not a hardcoded Active")
	assert.Equal(t, "essential_ed", events[0].Source)
	assert.True(t, events[0].IsCanvasEvent, "external events stay read-only on the calendar")
	assert.Equal(t, encodeCanvasClassID(2, 69742), events[0].CohortID, "click-through lands on the class")
	assert.Equal(t, march(2).Add(9*time.Hour), *events[0].StartTime)
	assert.Equal(t, march(9).Add(9*time.Hour), *events[1].StartTime)
}

// Only Essential Ed lists its own users. Canvas deliberately does not -- it
// keeps going through provider-middleware, so the user screens fall back for it.
func TestLiveUserListerFor(t *testing.T) {
	tests := []struct {
		name         string
		providerType ppapi.ProviderType
		isLister     bool
	}{
		{"essential ed lists its own users", ppapi.EssentialEd, true},
		{"canvas oss goes through provider-middleware", ppapi.CanvasOSS, false},
		{"canvas cloud goes through provider-middleware", ppapi.CanvasCloud, false},
		{"a non-live provider has no lister", ppapi.Kolibri, false},
	}
	srv := &server{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := srv.liveUserListerFor(&ppapi.Provider{Type: tt.providerType})
			assert.Equal(t, tt.isLister, ok)
		})
	}
}

// A 4xx body carries the reason; without it a caller only sees the status code.
func TestEssentialEdErrorIncludesResponseBody(t *testing.T) {
	srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"The roles parameter must be an array."}`))
	})
	defer done()

	transport := essentialEdTransport{srv: srv}
	_, err := transport.fetchAll(context.Background(), provider, provider.BaseURL+"/users", 0)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "422")
	assert.Contains(t, err.Error(), "must be an array")
}

// Essential Ed has no calendar endpoint, so the class lists build their schedule
// string from the weekly pattern on the class record itself.
func TestWeeklyScheduleClassEvents(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	assert.NoError(t, err)
	// 2026-03-04 is a Wednesday; the Monday that follows is 2026-03-09.
	now := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

	type wantEvent struct {
		rule     string
		duration string
	}
	tests := []struct {
		name     string
		schedule map[time.Weekday][]meetingTime
		loc      *time.Location
		want     []wantEvent
	}{
		{
			name: "weekdays that meet at the same time share one rule, in week order",
			schedule: map[time.Weekday][]meetingTime{
				time.Wednesday: {{540, 660}},
				time.Monday:    {{540, 660}},
			},
			loc: time.UTC,
			want: []wantEvent{
				{"DTSTART:20260309T090000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO,WE", "2h0m0s"},
			},
		},
		{
			name: "a weekday that meets at a different time keeps its own time and duration",
			schedule: map[time.Weekday][]meetingTime{
				time.Wednesday: {{600, 690}},
				time.Monday:    {{540, 660}},
			},
			loc: time.UTC,
			want: []wantEvent{
				{"DTSTART:20260309T090000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO", "2h0m0s"},
				// Each rule anchors on its own next occurrence, and now is a Wednesday.
				{"DTSTART:20260304T100000Z\nRRULE:FREQ=WEEKLY;BYDAY=WE", "1h30m0s"},
			},
		},
		{
			name:     "a second meeting on the same day is not dropped",
			schedule: map[time.Weekday][]meetingTime{time.Monday: {{540, 660}, {840, 900}}},
			loc:      time.UTC,
			want: []wantEvent{
				{"DTSTART:20260309T090000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO", "2h0m0s"},
				{"DTSTART:20260309T140000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO", "1h0m0s"},
			},
		},
		{
			name:     "the pattern's wall clock is anchored in the facility's zone",
			schedule: map[time.Weekday][]meetingTime{time.Monday: {{540, 660}}},
			loc:      chicago,
			want: []wantEvent{
				// 09:00 on 2026-03-09 is CDT, so 14:00Z -- DST began the day before.
				{"DTSTART:20260309T140000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO", "2h0m0s"},
			},
		},
		{
			name:     "a class with no pattern reports no schedule",
			schedule: nil,
			loc:      time.UTC,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			course := liveCourse{rawID: 69742, name: "College Prep I", weeklySchedule: tt.schedule}
			evs := weeklyScheduleClassEvents(course, tt.loc, now)
			assert.Len(t, evs, len(tt.want))
			for i, want := range tt.want {
				assert.Equal(t, want.rule, evs[i].RecurrenceRule)
				assert.Equal(t, want.duration, evs[i].Duration)
			}
		})
	}
}

// A resident's My Programs rows take the provider's own class status, not just
// the end date: Essential Ed marks a class finished with active=0 and frequently
// leaves close_date null.
func TestFetchLiveCoursesForUserHonoursProviderStatus(t *testing.T) {
	tests := []struct {
		name      string
		active    float64
		closeDate interface{}
		want      ProgramEnrollmentStatus
	}{
		{"active with no close date stays enrolled", 1, nil, Enrolled},
		{"inactive with no close date completes", 0, nil, EnrollmentCompleted},
		{"an elapsed close date still completes", 1, "2026-02-01 00:00:00", EnrollmentCompleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classes := []map[string]interface{}{
				{
					"id": float64(69742), "active": tt.active, "name": "College Prep I",
					"open_date": "2026-01-31 00:00:00", "close_date": tt.closeDate,
					"students": []interface{}{map[string]interface{}{"id": float64(4329946)}},
				},
			}
			srv, provider, done := essentialEdServer(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(envelope(classes, true))
			})
			defer done()

			rows := srv.fetchLiveCoursesForUser(provider, "4329946")
			assert.Len(t, rows, 1)
			assert.Equal(t, tt.want, rows[0].EnrollmentStatus)
		})
	}
}

// fakeProviders is Provider Platforms' api answering Provider from a map.
type fakeProviders struct {
	ppapi.ProviderPlatforms
	providers map[uint]ppapi.Provider
}

func (f fakeProviders) Provider(_ context.Context, id uint) (ppapi.Provider, error) {
	return f.providers[id], nil
}

// Only a provider that lists its own users (Essential Ed) answers; the rest
// fall back to provider-middleware.
func TestLiveProvidersUsers(t *testing.T) {
	status := http.StatusOK
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(envelope([]map[string]interface{}{
			{"id": float64(4329946), "first_name": "Jane", "last_name": "Smith", "username": "jsmith", "email_address": "jane@example.com"},
		}, true))
	}))
	defer ts.Close()
	const essentialEd, kolibri = 1, 2
	live := liveProviders{&server{
		Client: ts.Client(),
		providerPlatforms: fakeProviders{providers: map[uint]ppapi.Provider{
			essentialEd: {ID: essentialEd, Type: ppapi.EssentialEd, Name: "Essential Ed", AccountID: "303", AccessKey: "token", BaseURL: ts.URL},
			kolibri:     {ID: kolibri, Type: ppapi.Kolibri, Name: "Kolibri", BaseURL: ts.URL},
		}},
	}}

	t.Run("essential ed lists its users", func(t *testing.T) {
		users, ok, err := live.Users(context.Background(), essentialEd)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, []module.ProviderUser{{
			Username: "jsmith", NameFirst: "Jane", NameLast: "Smith", Email: "jane@example.com",
			ExternalUserID: "4329946", ExternalUsername: "jsmith",
		}}, users)
	})

	t.Run("kolibri goes through provider-middleware", func(t *testing.T) {
		users, ok, err := live.Users(context.Background(), kolibri)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, users)
	})

	t.Run("a failed listing is an error", func(t *testing.T) {
		status = http.StatusBadGateway
		_, ok, err := live.Users(context.Background(), essentialEd)
		assert.Error(t, err)
		assert.True(t, ok)
	})
}
