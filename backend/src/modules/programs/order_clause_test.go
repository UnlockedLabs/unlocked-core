package programs

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOrderClauseDirection(t *testing.T) {
	cases := []struct{ name, orderBy, order, want string }{
		{"asc", "name_last", "asc", "name_last asc"},
		{"desc", "name_last", "desc", "name_last desc"},
		{"upper case direction", "name_last", "DESC", "name_last desc"},
		{"empty direction defaults to asc", "name_last", "", "name_last asc"},
		{"subselect in direction", "name_last", "desc,(select 1/0)", "fb"},
		{"statement in direction", "name_last", "desc; drop table users", "fb"},
		{"unknown direction", "name_last", "sideways", "fb"},
		{"whitespace direction", "name_last", " ", "fb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := QueryContext{OrderBy: c.orderBy, Order: c.order}
			assert.Equal(t, c.want, q.OrderClause("fb", "name_last"))
		})
	}
}

func TestOrderClauseAllowlist(t *testing.T) {
	allowed := []string{"name_last", "ps.start_dt"}
	cases := []struct{ name, orderBy, order, want string }{
		{"allowed column", "name_last", "asc", "name_last asc"},
		{"qualified allowed column", "ps.start_dt", "desc", "ps.start_dt desc"},
		{"column not listed", "password", "asc", "fb"},
		{"empty column", "", "asc", "fb"},
		{"empty column and direction", "", "", "fb"},
		{"subselect as column", "(1/(select/**/0))", "", "fb"},
		{"column with direction smuggled in", "name_last desc", "", "fb"},
		{"comma separated columns", "name_last,password", "asc", "fb"},
		{"comment suffix", "name_last--", "asc", "fb"},
		{"upper case column is not a member", "NAME_LAST", "asc", "fb"},
		{"over long column", strings.Repeat("a", 10000), "asc", "fb"},
		{"NUL byte", "name_last\x00", "asc", "fb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := QueryContext{OrderBy: c.orderBy, Order: c.order}
			assert.Equal(t, c.want, q.OrderClause("fb", allowed...))
		})
	}
	assert.Equal(t, "fb", QueryContext{OrderBy: "name_last", Order: "asc"}.OrderClause("fb"))
}

// Sort keys the frontend and the trusted internal caller (api.go) send.
func TestOrderByListsKeepClientKeys(t *testing.T) {
	assert.Subset(t, programOverviewOrderBy, []string{"programs.name", "mr.total_active_enrollments", "completion_rate"})
	assert.Subset(t, programOrderBy, []string{"name"})
	assert.Subset(t, classDetailsOrderBy, []string{"ps.start_dt"})
	assert.Subset(t, enrollmentOrderBy, []string{"name_last", "doc_id", "start_dt"})
	assert.Subset(t, userProgramOrderBy, []string{"start_dt"})
}

// captureSQL is a store whose queries are built but not run, and the SQL each
// one produced. It lets the Postgres-only queries be checked on SQLite.
func captureSQL(t *testing.T) (*Store, *[]string) {
	t.Helper()
	db := newTestDB(t)
	var sqls []string
	record := func(tx *gorm.DB) {
		sqls = append(sqls, tx.Statement.SQL.String())
		// gorm skips this reset in DryRun, so a following Find would reuse the Count's SQL.
		tx.Statement.SQL.Reset()
		tx.Statement.Vars = nil
	}
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("capture", record))
	require.NoError(t, db.Callback().Row().After("gorm:row").Register("capture_row", record))
	return &Store{db.Session(&gorm.Session{DryRun: true})}, &sqls
}

func hostile(orderBy, order string) *QueryContext {
	return &QueryContext{Ctx: context.Background(), PerPage: 10, Page: 1, OrderBy: orderBy, Order: order}
}

func TestStoreQueriesIgnoreHostileOrder(t *testing.T) {
	const payload = "(1/(select/**/0))"
	cases := []struct {
		name string
		run  func(s *Store, q *QueryContext)
		want string
	}{
		{"programs", func(s *Store, q *QueryContext) { _, _ = s.GetPrograms(q) }, "programs.created_at desc"},
		{"programs overview", func(s *Store, q *QueryContext) {
			_, _ = s.GetProgramsOverviewTable(q, 30, false, "", nil)
		}, "programs.created_at desc"},
		{"class details", func(s *Store, q *QueryContext) { _, _ = s.GetProgramClassDetailsByID(1, q) }, "ps.created_at desc"},
		{"class outcomes", func(s *Store, q *QueryContext) { _, _ = s.GetProgramClassOutcomes(1, q) }, "months.month asc"},
		{"class history", func(s *Store, q *QueryContext) { _, _ = s.GetProgramClassesHistory(1, "t", q) }, "created_at desc"},
		{"user programs", func(s *Store, q *QueryContext) { _, _ = s.GetUserProgramInfo(q, 1) }, "pce.created_at desc"},
		{"enrollments", func(s *Store, q *QueryContext) { _, _ = s.GetProgramClassEnrollmentsForProgram(q, 1, "all") }, "pse.created_at desc"},
		{"attendees", func(s *Store, q *QueryContext) { _, _ = s.GetAttendees(q, nil, 1) }, "program_class_event_attendance.created_at DESC"},
	}
	for _, c := range cases {
		for _, in := range [][2]string{{payload, ""}, {"name_last", "desc," + payload}} {
			t.Run(c.name+"/"+in[0]+" "+in[1], func(t *testing.T) {
				s, sqls := captureSQL(t)
				c.run(s, hostile(in[0], in[1]))
				require.NotEmpty(t, *sqls, "query was not built")
				for _, q := range *sqls {
					assert.NotContains(t, q, "select/**/0")
				}
				assert.True(t, slices.ContainsFunc(*sqls, func(q string) bool { return strings.Contains(q, "ORDER BY "+c.want) }),
					"no query ordered by %q", c.want)
			})
		}
	}
}

// The roster query builds its ORDER BY into a raw SQL string.
func TestRosterOrderIgnoresHostileOrder(t *testing.T) {
	const payload = "(1/(select/**/0))"
	cases := []struct{ orderBy, order, want string }{
		{"name_last", "asc", "name_last asc, name_first"},
		{"name_first", "desc", "name_first desc"},
		{"name_last", "desc," + payload, "e.created_at DESC"},
		{"name_last", "desc;" + payload, "e.created_at DESC"},
		{payload, "", "e.id ASC"},
		{"name_last, " + payload, "asc", "e.id ASC"},
	}
	for _, c := range cases {
		t.Run(c.orderBy+" "+c.order, func(t *testing.T) {
			assert.Equal(t, c.want, rosterOrder(hostile(c.orderBy, c.order)))
		})
	}
}
