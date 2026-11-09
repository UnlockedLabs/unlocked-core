package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// order_by=<col> <dir> in one param is split into the two fields the
// database layer validates, so it keeps working after the allowlist.
func TestGetQueryContextSplitsOrderBy(t *testing.T) {
	cases := []struct{ query, orderBy, order string }{
		{"order_by=name_last%20desc", "name_last", "desc"},
		{"order_by=NAME_LAST%20DESC", "name_last", "desc"},
		{"order_by=name_last&order=asc", "name_last", "asc"},
		{"order_by=ps.start_dt%20asc", "ps.start_dt", "asc"},
	}
	srv := &Server{}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/users?"+c.query, nil)
			r = r.WithContext(context.WithValue(r.Context(), ClaimsKey, &Claims{}))
			qc := srv.getQueryContext(r)
			assert.Equal(t, c.orderBy, qc.OrderBy)
			assert.Equal(t, c.order, qc.Order)
			assert.Equal(t, c.orderBy+" "+c.order, qc.OrderClause("fb", c.orderBy))
		})
	}
}
