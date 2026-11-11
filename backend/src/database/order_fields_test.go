package database

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Sort keys the frontend sends to the users list and the resident filters
// (StudentManagement.tsx, AddClassEnrollments.tsx, EnrollResidentsModal.tsx).
func TestOrderByFieldsKeepClientKeys(t *testing.T) {
	assert.Subset(t, validUserOrderByFields, []string{"name_last", "username", "doc_id", "last_login", "created_at"})
	assert.Subset(t, residentOrderByFields, []string{"name_last", "doc_id", "created_at"})
}

// captureUserQueries is a DB whose queries are built but not run, and the SQL
// each one produced.
func captureUserQueries(t *testing.T) (*DB, *[]string) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory"), &gorm.Config{DryRun: true})
	require.NoError(t, err)
	var sqls []string
	record := func(tx *gorm.DB) {
		sqls = append(sqls, tx.Statement.SQL.String())
		// gorm skips this reset in DryRun, so a following query would reuse this SQL.
		tx.Statement.SQL.Reset()
		tx.Statement.Vars = nil
	}
	require.NoError(t, gdb.Callback().Query().After("gorm:query").Register("capture", record))
	return NewDB(gdb), &sqls
}

func TestUserListsIgnoreHostileOrder(t *testing.T) {
	const payload = "(1/(select/**/0))"
	args := func(orderBy, order string) *models.QueryContext {
		return &models.QueryContext{Ctx: context.Background(), PerPage: 10, Page: 1, OrderBy: orderBy, Order: order}
	}
	cases := []struct {
		name, orderBy, order, want string
		run                        func(db *DB, q *models.QueryContext)
	}{
		{"users list direction", "name_last", "desc," + payload, "ORDER BY users.name_last desc", func(db *DB, q *models.QueryContext) { _, _ = db.GetCurrentUsers(q, "student") }},
		{"users list column", payload, "", "ORDER BY name_last asc", func(db *DB, q *models.QueryContext) { _, _ = db.GetCurrentUsers(q, "student") }},
		{"users list legit", "last_login", "desc", "login_metrics.last_login desc", func(db *DB, q *models.QueryContext) { _, _ = db.GetCurrentUsers(q, "student") }},
		{"residents direction", "name_last", "asc," + payload, "ORDER BY users.created_at desc", func(db *DB, q *models.QueryContext) {
			_, _ = db.GetResidentsByIDs(q, []uint{1}, "users.created_at desc")
		}},
		{"residents column", payload, "asc", "ORDER BY users.created_at desc", func(db *DB, q *models.QueryContext) {
			_, _ = db.GetResidentsByIDs(q, []uint{1}, "users.created_at desc")
		}},
		{"residents joinless column", "last_login", "asc", "ORDER BY users.created_at desc", func(db *DB, q *models.QueryContext) {
			_, _ = db.GetResidentsByIDs(q, []uint{1}, "users.created_at desc")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, sqls := captureUserQueries(t)
			c.run(db, args(c.orderBy, c.order))
			require.NotEmpty(t, *sqls, "query was not built")
			assert.True(t, slices.ContainsFunc(*sqls, func(q string) bool { return strings.Contains(q, c.want) }), "no query with %q in %v", c.want, *sqls)
			for _, q := range *sqls {
				assert.NotContains(t, q, "select/**/0")
			}
		})
	}
}
