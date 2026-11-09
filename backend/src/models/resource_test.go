package models

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
		{"subselect as column", "(select 1/0)", "", "fb"},
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
}

func TestOrderClauseNoAllowlistFallsBack(t *testing.T) {
	assert.Equal(t, "fb", QueryContext{OrderBy: "name_last", Order: "asc"}.OrderClause("fb"))
}
