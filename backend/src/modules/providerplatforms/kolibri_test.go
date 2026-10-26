package providerplatforms

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newKolibriDB returns an in-memory stand-in for Kolibri's own database,
// with the columns the Kolibri service reads.
func newKolibriDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:kolibri_%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	kdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	for _, stmt := range []string{
		`CREATE TABLE kolibriauth_facilityuser (id text, full_name text, username text, facility_id text)`,
		`CREATE TABLE content_channelmetadata (id text, name text, description text, thumbnail text, total_resource_count integer, root_id text)`,
		`CREATE TABLE kolibriauth_collection (id text, name text, kind text)`,
		`CREATE TABLE kolibriauth_membership (id text, user_id text, collection_id text)`,
		`CREATE TABLE lessons_lesson (id text, collection_id text, is_active text, date_created text)`,
		`CREATE TABLE lessons_lessonassignment (id text, lesson_id text, collection_id text)`,
		`CREATE TABLE exams_exam (id text, collection_id text)`,
		`CREATE TABLE logger_contentsessionlog (id text, channel_id text, user_id text, start_timestamp text)`,
		`CREATE TABLE logger_contentsummarylog (id text, user_id text, time_spent text, completion_timestamp text, content_id text, progress text, kind text, channel_id text)`,
		`CREATE TABLE notifications_learnerprogressnotification (id integer, user_id text, classroom_id text, notification_object text, notification_event text, quiz_num_correct integer, quiz_num_answered integer)`,
	} {
		require.NoError(t, kdb.Exec(stmt).Error)
	}
	return kdb
}

func newTestKolibriService(t *testing.T, db *gorm.DB) (*KolibriService, *gorm.DB) {
	t.Helper()
	p := createProvider(t, db, ProviderPlatform{
		Type:      Kolibri,
		Name:      "Kolibri",
		AccountID: "facility-1",
		BaseUrl:   "http://kolibri.local",
	})
	kdb := newKolibriDB(t)
	return &KolibriService{ProviderPlatformID: p.ID, AccountID: p.AccountID, BaseURL: p.BaseUrl, db: kdb}, kdb
}

func TestKolibriGetUsers(t *testing.T) {
	db := newTestDB(t)
	ks, kdb := newTestKolibriService(t, db)
	require.NoError(t, kdb.Exec(`INSERT INTO kolibriauth_facilityuser VALUES
		('u1', 'Ada Lovelace', 'ada', 'facility-1'),
		('u2', 'Plato', 'plato', 'facility-1'),
		('u3', 'Mapped User', 'mapped', 'facility-1'),
		('u4', 'Other Place', 'other', 'facility-2')`).Error)
	createResident(t, db, ks.ProviderPlatformID, 1, "student", "u3")

	users, err := ks.GetUsers(db)
	require.NoError(t, err)
	assert.Equal(t, []ImportUser{
		{NameFirst: "Ada", NameLast: "Lovelace", Username: "ada", Email: "ada@unlocked.v2", ExternalUserID: "u1"},
		{NameFirst: "Plato", NameLast: "", Username: "plato", Email: "plato@unlocked.v2", ExternalUserID: "u2"},
	}, users)
}

func TestKolibriImportCourses(t *testing.T) {
	db := newTestDB(t)
	core := newFakeCore(t)
	ks, kdb := newTestKolibriService(t, db)
	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png"))
	require.NoError(t, kdb.Exec(`INSERT INTO content_channelmetadata VALUES
		('aaaa-1111', 'Khan', ?, ?, 40, 'root-1'),
		('bbbb-2222', 'Old channel', '', '', 9, 'root-2')`, strings.Repeat("d", 300), png).Error)
	require.NoError(t, kdb.Exec(`INSERT INTO kolibriauth_collection VALUES
		('cccc-3333', 'Class A', 'classroom'),
		('dddd-4444', 'Class B', 'classroom'),
		('eeee-5555', 'Facility', 'facility')`).Error)
	require.NoError(t, kdb.Exec(`INSERT INTO lessons_lesson VALUES
		('l1', 'cccc-3333', 'true', ''), ('l2', 'cccc-3333', 'true', ''), ('l3', 'dddd-4444', 'true', '')`).Error)
	require.NoError(t, kdb.Exec(`INSERT INTO exams_exam VALUES ('x1', 'cccc-3333')`).Error)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: ks.ProviderPlatformID, ExternalID: "bbbb-2222", Name: "Old channel", TotalProgressMilestones: 1}).Error)
	require.NoError(t, db.Create(&Course{ProviderPlatformID: ks.ProviderPlatformID, ExternalID: "dddd-4444", Name: "Class B", TotalProgressMilestones: 7}).Error)

	require.NoError(t, ks.ImportCourses(db))

	var courses []Course
	require.NoError(t, db.Order("external_id").Find(&courses).Error)
	require.Len(t, courses, 4)
	khan, old, classA, classB := courses[0], courses[1], courses[2], courses[3]

	assert.Equal(t, "Khan", khan.Name)
	assert.Equal(t, CourseType("open_content"), khan.Type)
	assert.Equal(t, stringPtr("completion"), khan.OutcomeTypes)
	assert.Equal(t, stringPtr(strings.Repeat("d", 255)), khan.Description)
	assert.Equal(t, stringPtr("/api/photos/aaaa-1111.png"), khan.ThumbnailURL)
	assert.Equal(t, stringPtr("http://kolibri.local/en/learn/%23/topics/t/aaaa1111/folders%3Flast=HOME"), khan.ExternalURL)
	assert.Equal(t, uint(40), khan.TotalProgressMilestones)

	assert.Equal(t, uint(1), old.TotalProgressMilestones, "an existing channel isn't updated")

	assert.Equal(t, "Class A", classA.Name)
	assert.Equal(t, stringPtr("Kolibri managed course"), classA.Description)
	assert.Nil(t, classA.ThumbnailURL)
	assert.Equal(t, stringPtr("http://kolibri.local/en/learn/%23/home/classes/cccc3333"), classA.ExternalURL)
	assert.Equal(t, uint(3), classA.TotalProgressMilestones, "two lessons and one exam")

	assert.Equal(t, uint(1), classB.TotalProgressMilestones, "an existing class gets its new total")

	// As with Brightspace, the multipart reader keeps only the base name of
	// image_root-1/aaaa-1111.png.
	assert.Equal(t, []string{"aaaa-1111.png"}, core.Uploads())
}

// The enrollment milestones come from a learner's first session on a
// channel, or their first active lesson in a class.
func TestKolibriEnrollmentMilestones(t *testing.T) {
	db := newTestDB(t)
	ks, kdb := newTestKolibriService(t, db)
	require.NoError(t, kdb.Exec(`INSERT INTO kolibriauth_facilityuser VALUES ('u1', 'A', 'a', 'facility-1'), ('u2', 'B', 'b', 'facility-1')`).Error)
	require.NoError(t, kdb.Exec(`INSERT INTO content_channelmetadata VALUES ('ch', 'Khan', '', '', 1, 'ch')`).Error)
	require.NoError(t, kdb.Exec(`INSERT INTO logger_contentsessionlog VALUES ('s1', 'ch', 'u1', '2026-03-01'), ('s2', 'ch', 'u1', '2026-03-02'), ('s3', 'other', 'u2', '2026-03-01')`).Error)
	po := milestonePO{
		course:          map[string]any{"course_id": int64(5), "external_course_id": "ch"},
		usersMap:        map[string]uint{"u1": 101, "u2": 102},
		externalUserIds: []string{"u1", "u2"},
	}

	require.NoError(t, importEnrollmentMilestones(ks, po, db))
	require.NoError(t, importEnrollmentMilestones(ks, po, db))

	var enrolled []uint
	require.NoError(t, db.Model(&UserEnrollment{}).Pluck("user_id", &enrolled).Error)
	assert.Equal(t, []uint{101}, enrolled)
}

// The activity import skips activity it has recorded before and rows it
// can't read, and records the rest through a Postgres procedure. It finds
// the user by external id without checking the provider.
func TestKolibriActivity(t *testing.T) {
	db := newTestDB(t)
	ks, kdb := newTestKolibriService(t, db)
	require.NoError(t, kdb.Exec(`INSERT INTO logger_contentsummarylog VALUES
		('a1', 'u1', '12.5', '', 'c', '1', 'video', 'ch'),
		('a2', 'u1', 'bad', '', 'c', '1', 'video', 'ch'),
		('a3', 'u9', '1', '', 'c', '1', 'html5', 'ch'),
		('a4', 'u1', '3', '', 'c', '1', 'exercise', 'ch'),
		('a5', 'u1', '4', '', 'c', '1', 'audio', 'ch'),
		('a6', 'u8', '4', '', 'c', '1', 'video', 'ch'),
		('a7', 'u1', '5', '', 'c', '1', 'video', 'other')`).Error)
	createResident(t, db, ks.ProviderPlatformID, 101, "student", "u1")
	other := createProvider(t, db, ProviderPlatform{Type: Kolibri, Name: "other"})
	createResident(t, db, other.ID, 109, "student", "u9")
	require.NoError(t, db.Exec(`INSERT INTO activities (course_id, user_id, type, total_time, time_delta, external_id) VALUES (5, 101, 'content_interaction', 1, 1, 'a1')`).Error)
	calls := recordCalls(t, db)

	require.NoError(t, ks.ImportActivityForCourse(map[string]any{"course_id": int64(5), "external_course_id": "ch"}, db))

	assert.Equal(t, []procedureCall{
		{"insert_daily_activity_kolibri", []any{uint(109), 5, ContentInteraction, 1.0, "a3"}},
		{"insert_daily_activity_kolibri", []any{uint(101), 5, CourseInteraction, 3.0, "a4"}},
		{"insert_daily_activity_kolibri", []any{uint(101), 5, ContentInteraction, 4.0, "a5"}},
	}, *calls)
}

func TestKolibriHelpers(t *testing.T) {
	assert.True(t, IsNumber("12"))
	assert.False(t, IsNumber("u1"))
	assert.Equal(t, "abcd1234", stripUuidForUrl("abcd-1234"))
	assert.Equal(t, []byte("png"), decodeImg("data:image/png;base64,"+base64.StdEncoding.EncodeToString([]byte("png"))))
	assert.Nil(t, decodeImg("https://example.com/x.png"))
}
