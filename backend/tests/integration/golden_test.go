package integration

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/require"
)

// The helpers the route tests share: golden files, a client that doesn't
// follow redirects, and claims with features turned off.

// updateGolden rewrites the golden files in testdata instead of comparing
// with them, after an intended change:
//
//	go test ./tests/integration -run '<Test>' -update
var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// noRedirects is env's client without following redirects, so a test sees
// the redirect a route answers with.
func noRedirects(env *TestEnv) *Client {
	return &Client{baseURL: env.Client.baseURL, httpClient: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// withFeatures returns the claims with every feature on except the ones left
// out.
func withFeatures(c *handlers.Claims, off ...models.FeatureAccess) *handlers.Claims {
	cp := *c
	cp.FeatureAccess = []models.FeatureAccess{}
	for _, f := range features.All {
		if !slices.Contains(off, f) {
			cp.FeatureAccess = append(cp.FeatureAccess, f)
		}
	}
	return &cp
}

// timeKey matches the JSON fields that hold a time that changes every run.
var timeKey = regexp.MustCompile(`(_at|_ts)$`)

// expectGolden compares body with testdata/<dir>/<name>.golden. A JSON body is
// compared after indenting it and replacing its times with "<time>".
func expectGolden(t *testing.T, dir, name, body string) {
	t.Helper()
	got := normalizeBody(t, body)
	path := filepath.Join("testdata", dir, name+".golden")
	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden file; run with -update to create it")
	require.Equal(t, string(want), got)
}

func normalizeBody(t *testing.T, body string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return body
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(maskTimes(v)))
	return buf.String()
}

// maskTimes replaces every time with "<time>", except a zero time, which is
// kept because it shows a field the route never set.
func maskTimes(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && timeKey.MatchString(k) && !strings.HasPrefix(s, "0001-01-01") {
				v[k] = "<time>"
			} else {
				v[k] = maskTimes(x)
			}
		}
	case []any:
		for i, x := range v {
			v[i] = maskTimes(x)
		}
	}
	return v
}
