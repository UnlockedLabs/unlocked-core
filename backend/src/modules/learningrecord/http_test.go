package learningrecord

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed request's error line carries core's fields, such as the admin on
// an admin write, as core's own handlers' error lines do.
func TestHandleErrorLogFields(t *testing.T) {
	oldHooks := logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	defer logrus.StandardLogger().ReplaceHooks(oldHooks)
	hook := logtest.NewGlobal()

	core := map[string]any{"admin_id": uint(3), "username": "facadmin", "handler": "NameNotFound"}
	ctx := module.WithLog(context.Background(), core, func(string, any) {})
	r := httptest.NewRequestWithContext(ctx, http.MethodPut, "/api/learning-record/entries/1", nil)
	w := httptest.NewRecorder()
	handle(func(http.ResponseWriter, *http.Request, sLog) error {
		return errors.New("boom")
	}).ServeHTTP(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	entry := hook.LastEntry()
	require.NotNil(t, entry)
	assert.Equal(t, logrus.ErrorLevel, entry.Level)
	assert.Equal(t, uint(3), entry.Data["admin_id"])
	assert.Equal(t, "facadmin", entry.Data["username"])
	assert.Equal(t, "learningrecord", entry.Data["module"])
	assert.Equal(t, http.MethodPut, entry.Data["method"])
	assert.Equal(t, "/api/learning-record/entries/1", entry.Data["path"])
}
