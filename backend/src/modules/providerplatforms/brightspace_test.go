package providerplatforms

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeBrightspace stands in for Brightspace's token endpoint, its bulk data
// sets and a tenant's course API. The token and data set URLs are fixed in
// brightspace.go, so routeHosts sends them here.
type fakeBrightspace struct {
	*httptest.Server
	// datasets maps a data set's name to the CSV it downloads as.
	datasets map[string]string
	// tokenStatus and tokenBody answer the token refresh.
	tokenStatus int
	tokenBody   map[string]any
	// images and offerings are the course API's answers, by course id.
	images    map[string][]byte
	offerings map[string]CourseOffering

	mu        sync.Mutex
	tokenForm url.Values
	downloads []string
}

func newFakeBrightspace(t *testing.T) *fakeBrightspace {
	t.Helper()
	fb := &fakeBrightspace{
		datasets:    map[string]string{},
		tokenStatus: http.StatusOK,
		tokenBody:   map[string]any{"access_token": "access-2", "refresh_token": "refresh-2"},
		images:      map[string][]byte{},
		offerings:   map[string]CourseOffering{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /core/connect/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fb.mu.Lock()
		fb.tokenForm = r.PostForm
		fb.mu.Unlock()
		w.WriteHeader(fb.tokenStatus)
		_ = json.NewEncoder(w).Encode(fb.tokenBody)
	})
	mux.HandleFunc("GET /d2l/api/lp/1.31/dataExport/bds/list", func(w http.ResponseWriter, r *http.Request) {
		plugins := []DataSetPlugin{}
		for name := range fb.datasets {
			plugins = append(plugins, DataSetPlugin{PluginId: "plugin-" + name, Name: name})
		}
		_ = json.NewEncoder(w).Encode(plugins)
	})
	mux.HandleFunc("GET /d2l/api/lp/1.31/dataExport/bds/download/{plugin}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("plugin")[len("plugin-"):]
		csv, ok := fb.datasets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fb.mu.Lock()
		fb.downloads = append(fb.downloads, name)
		fb.mu.Unlock()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		f, _ := zw.Create(name + ".csv")
		_, _ = f.Write([]byte(csv))
		_ = zw.Close()
		_, _ = w.Write(buf.Bytes())
	})
	mux.HandleFunc("GET /d2l/api/lp/1.31/courses/{id}/image", func(w http.ResponseWriter, r *http.Request) {
		img, ok := fb.images[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(img)
	})
	mux.HandleFunc("GET /d2l/api/lp/1.31/courses/{id}", func(w http.ResponseWriter, r *http.Request) {
		offering, ok := fb.offerings[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(offering)
	})
	fb.Server = httptest.NewServer(mux)
	t.Cleanup(fb.Close)
	routeHosts(t, fb.URL, "auth.brightspace.com", "unlocked.brightspacedemo.com")
	return fb
}

func (fb *fakeBrightspace) Downloads() []string {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]string(nil), fb.downloads...)
}

// useTempDir points the Brightspace download folder at a new temp dir.
func useTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := CsvDownloadPath
	CsvDownloadPath = dir
	t.Cleanup(func() { CsvDownloadPath = orig })
	return dir
}

func createBrightspaceProvider(t *testing.T, db *gorm.DB, fb *fakeBrightspace) *ProviderPlatform {
	t.Helper()
	return createProvider(t, db, ProviderPlatform{
		Type:      Brightspace,
		Name:      "Brightspace",
		AccountID: "client-id",
		AccessKey: "secret;refresh-1",
		BaseUrl:   fb.URL,
	})
}

func newTestBrightspaceService(t *testing.T, db *gorm.DB, p *ProviderPlatform) *BrightspaceService {
	t.Helper()
	srv, err := newBrightspaceService(p, db, nil)
	require.NoError(t, err)
	return srv
}

// Starting the service refreshes the OAuth token and saves the new refresh
// token on the provider. It saves the access key as it is in memory, which
// AfterFind has decrypted (the same problem as EN-230).
func TestBrightspaceTokenRefresh(t *testing.T) {
	db := newTestDB(t)
	useTempDir(t)
	fb := newFakeBrightspace(t)
	created := createBrightspaceProvider(t, db, fb)
	var p ProviderPlatform
	require.NoError(t, db.First(&p, created.ID).Error)

	srv := newTestBrightspaceService(t, db, &p)

	assert.Equal(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {"refresh-1"},
		"client_id":     {"client-id"},
		"client_secret": {"secret"},
		"scope":         {BrightspaceScopes},
	}, fb.tokenForm)
	assert.Equal(t, "Bearer access-2", srv.BaseHeaders["Authorization"])
	var stored string
	require.NoError(t, db.Table("provider_platforms").Where("id = ?", p.ID).Pluck("access_key", &stored).Error)
	assert.Equal(t, "secret;refresh-2", stored)
}

func TestBrightspaceStartErrors(t *testing.T) {
	db := newTestDB(t)
	dir := useTempDir(t)
	fb := newFakeBrightspace(t)

	p := createBrightspaceProvider(t, db, fb)
	p.AccessKey = "no-refresh-token"
	_, err := newBrightspaceService(p, db, nil)
	assert.EqualError(t, err, "unable to find refresh token, unable to intialize BrightspaceService")

	CsvDownloadPath = filepath.Join(dir, "missing")
	_, err = newBrightspaceService(createBrightspaceProvider(t, db, fb), db, nil)
	assert.EqualError(t, err, "unable to find csv download directory path "+CsvDownloadPath+", unable to intialize BrightspaceService")
	CsvDownloadPath = dir

	fb.tokenStatus = http.StatusBadRequest
	fb.tokenBody = map[string]any{"error": "invalid_grant", "error_description": "expired"}
	_, err = newBrightspaceService(createBrightspaceProvider(t, db, fb), db, nil)
	assert.EqualError(t, err, "unable to request new refresh token from brightspace, response error message is: invalid_grant: expired")

	fb.tokenBody = map[string]any{}
	_, err = newBrightspaceService(createBrightspaceProvider(t, db, fb), db, nil)
	assert.EqualError(t, err, "unable to request new refresh token from brightspace")
}

func TestBrightspaceGetUsers(t *testing.T) {
	db := newTestDB(t)
	dir := useTempDir(t)
	fb := newFakeBrightspace(t)
	fb.datasets["Users"] = "UserId,UserName,FirstName,LastName,IsActive,ExternalEmail\n" +
		"1,ada,Ada,Lovelace,True,ada@example.com\n" +
		"2,old,Old,User,False,old@example.com\n" +
		"3,mapped,Mapped,User,TRUE,mapped@example.com\n"
	p := createBrightspaceProvider(t, db, fb)
	createResident(t, db, p.ID, 1, "student", "3")

	users, err := newTestBrightspaceService(t, db, p).GetUsers(db)
	require.NoError(t, err)
	assert.Equal(t, []ImportUser{
		{Username: "ada", NameFirst: "Ada", NameLast: "Lovelace", Email: "ada@example.com", ExternalUserID: "1"},
	}, users)
	assert.NoFileExists(t, filepath.Join(dir, "users", "Users.csv"))
	assert.NoFileExists(t, filepath.Join(dir, "users", "Users.zip"))
}

func TestBrightspaceImportCourses(t *testing.T) {
	db := newTestDB(t)
	useTempDir(t)
	core := newFakeCore(t)
	fb := newFakeBrightspace(t)
	fb.datasets["Organizational Units"] = "OrgUnitId,Type,Name,Code,IsActive,IsDeleted,StartDate,EndDate\n" +
		"10,Course Offering,Algebra,ALG,True,False,2026-01-05T08:00:00.000Z,\n" +
		"11,Course Offering,Biology,,True,False,,\n" +
		"12,Course Offering,Chemistry,CHEM,True,False,,\n" +
		"13,Course Offering,Inactive,,False,False,,\n" +
		"14,Course Offering,Deleted,,True,True,,\n" +
		"15,Department,Science,,True,False,,\n" +
		"16,Course Offering,Empty,,True,False,,\n" +
		"17,Course Offering,Existing,,True,False,,\n"
	fb.datasets["Content Objects"] = "ContentObjectId,OrgUnitId\n" +
		"a,10\nb,10\nc,11\nd,12\ne,13\nf,14\ng,15\nh,17\ni,17\nj,17\n"
	fb.images["10"] = []byte("png")
	fb.offerings["10"] = CourseOffering{Description: RichText{Text: "Numbers"}}
	fb.offerings["11"] = CourseOffering{Description: RichText{Html: "<p>Cells <b>live</b></p>"}}
	p := createBrightspaceProvider(t, db, fb)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: p.ID, ExternalID: "17", Name: "Existing", TotalProgressMilestones: 1}).Error)

	require.NoError(t, newTestBrightspaceService(t, db, p).ImportCourses(db))

	var courses []Course
	require.NoError(t, db.Order("external_id").Find(&courses).Error)
	require.Len(t, courses, 4)
	alg, bio, chem, existing := courses[0], courses[1], courses[2], courses[3]

	assert.Equal(t, "Algebra", alg.Name)
	assert.Equal(t, stringPtr("ALG"), alg.AltName)
	assert.Equal(t, stringPtr("Numbers"), alg.Description)
	// The upload is named image_brightspace/10.jpg, but a multipart reader
	// keeps only the base name, as core's /upload does.
	assert.Equal(t, stringPtr("/api/photos/10.jpg"), alg.ThumbnailURL)
	assert.Equal(t, stringPtr("completion"), alg.OutcomeTypes)
	assert.Equal(t, CourseType("fixed_enrollment"), alg.Type)
	assert.Equal(t, uint(2), alg.TotalProgressMilestones)
	assert.Equal(t, stringPtr(fb.URL+"/d2l/le/sequenceLauncher/10/View"), alg.ExternalURL)
	assert.Equal(t, "2026-01-05", alg.StartDt.Format("2006-01-02"))
	assert.Nil(t, alg.EndDt)

	assert.Nil(t, bio.AltName)
	assert.Equal(t, stringPtr("Cells live"), bio.Description)
	assert.Nil(t, bio.ThumbnailURL, "no image and no fallback when the image request isn't a 200")

	assert.Equal(t, stringPtr("Brightspace Managed Course: Chemistry"), chem.Description)

	assert.Equal(t, "Existing", existing.Name)
	assert.Equal(t, uint(3), existing.TotalProgressMilestones)

	assert.Equal(t, []string{"10.jpg"}, core.Uploads())
}

func TestBrightspaceImportMilestones(t *testing.T) {
	db := newTestDB(t)
	useTempDir(t)
	fb := newFakeBrightspace(t)
	fb.datasets["User Enrollments"] = "OrgUnitId,UserId\n10,1\n10,2\n10,9\n11,1\n"
	fb.datasets["Assignment Submissions"] = "DropboxId,OrgUnitId,SubmitterId,IsGraded\n" +
		"d1,10,1,False\n" +
		"d2,10,2,True\n" +
		"d3,10,1,True\n" +
		"d4,10,9,False\n"
	fb.datasets["Quiz Attempts"] = "AttemptId,UserId,OrgUnitId,IsGraded,IsDeleted\n" +
		"q1,1,10,False,False\n" +
		"q2,2,10,True,False\n" +
		"q3,1,10,False,True\n"
	p := createBrightspaceProvider(t, db, fb)
	srv := newTestBrightspaceService(t, db, p)
	users := []map[string]any{
		{"user_id": int64(101), "external_user_id": "1"},
		{"user_id": int64(102), "external_user_id": "2"},
	}
	course := map[string]any{"course_id": int64(5), "external_course_id": "10"}

	require.NoError(t, srv.ImportMilestones(course, users, db, lastRun))

	var enrolled []uint
	require.NoError(t, db.Model(&UserEnrollment{}).Order("user_id").Pluck("user_id", &enrolled).Error)
	assert.Equal(t, []uint{101, 102}, enrolled)

	type row struct {
		UserID     uint
		ExternalID string
		Type       string
	}
	var got []row
	require.NoError(t, db.Model(&Milestone{}).Order("id").Find(&got).Error)
	// The graded ids start with the user id of the row before (0 for the
	// first), because gradedId is built before userId is set.
	assert.Equal(t, []row{
		{101, "d1-10-1", "assignment_submission"},
		{102, "101-assign-d2-10-2", "grade_received"},
		{101, "102-assign-d3-10-1", "grade_received"},
		{101, "q1", "quiz_submission"},
		{102, "101-quiz-q2", "grade_received"},
	}, got)

	// Later courses in the same job reuse the downloaded files.
	require.NoError(t, srv.ImportMilestones(map[string]any{"course_id": int64(6), "external_course_id": "11"}, users, db, lastRun))
	assert.Equal(t, []string{"User Enrollments", "Assignment Submissions", "Quiz Attempts"}, fb.Downloads())
	require.NoError(t, db.Model(&UserEnrollment{}).Where("course_id = 6").Pluck("user_id", &enrolled).Error)
	assert.Equal(t, []uint{101}, enrolled)
}

// The activity import records each mapped user's time on the course's
// content through a Postgres procedure.
func TestBrightspaceActivity(t *testing.T) {
	db := newTestDB(t)
	useTempDir(t)
	fb := newFakeBrightspace(t)
	fb.datasets["Content Objects"] = "ContentObjectId,OrgUnitId,Title,ContentObjectType,IsDeleted\n" +
		"c1,10,Intro,Topic,0\n" +
		"c2,10,Gone,Topic,1\n" +
		"c3,11,Other,Topic,0\n"
	fb.datasets["Content User Progress"] = "ContentObjectId,UserId,LastVisited,TotalTime,Version\n" +
		"c1,1,2026-03-02,120,1\n" +
		"c2,1,2026-03-02,60,1\n" +
		"c1,9,2026-03-02,30,1\n"
	p := createBrightspaceProvider(t, db, fb)
	createResident(t, db, p.ID, 101, "student", "1")
	srv := newTestBrightspaceService(t, db, p)

	got := srv.getUsersActivity(db, "10", writeDataset(t, fb, "Content Objects"), writeDataset(t, fb, "Content User Progress"))
	assert.Equal(t, map[uint][]BrightspaceUserContentDto{
		101: {{OrgUnitId: "10", ContentObjectId: "c1", TotalTime: "120", Title: "Intro", ContentObjectType: "Topic", ExternalId: "1-c1-1-2026-03-02"}},
	}, got)

	calls := recordCalls(t, db)
	require.NoError(t, srv.ImportActivityForCourse(map[string]any{"course_id": int64(5), "external_course_id": "10"}, db))
	assert.Equal(t, []procedureCall{
		{"insert_daily_activity_brightspace", []any{uint(101), uint(5), ContentInteraction, "120", "1-c1-1-2026-03-02"}},
	}, *calls)
	assert.True(t, srv.IsDownloaded)
	assert.Equal(t, []string{"Content Objects", "Content User Progress"}, fb.Downloads())
}

func writeDataset(t *testing.T, fb *fakeBrightspace, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.csv")
	require.NoError(t, os.WriteFile(path, []byte(fb.datasets[name]), 0o600))
	return path
}
