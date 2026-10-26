package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// Reading a provider's classes is slow -- a course listing plus an enrollment
// count per course, multiplied by every facility when the index is statewide.
// Doing that on the request path made the classes page wait on the provider
// before showing the classes we already have in our own tables. Instead the
// result is cached in NATS KV and refreshed in the background, exactly as
// getCanvasProviderPrograms does for the programs page.

// canvasClassCacheTTL is how long a cached class listing is served before it is
// refreshed. It matches the bucket TTL.
const canvasClassCacheTTL = 5 * time.Minute

type CachedCanvasClasses struct {
	Classes     []ProgramClassCohort
	LastUpdated time.Time
	Loading     bool
}

// canvasClassCacheKey scopes the cache to the facility being viewed. The
// statewide index builds facility-scoped IDs for every facility, so it cannot
// share an entry with a single-facility view.
func canvasClassCacheKey(facilityID *uint) string {
	if facilityID == nil {
		return "canvas_classes_all"
	}
	return fmt.Sprintf("canvas_classes_%d", *facilityID)
}

// getCanvasProviderClasses returns the cached provider classes for this view.
// A fresh entry is returned as-is. On a miss or a stale entry it returns
// loading=true and refreshes in the background, so the caller can answer with
// the classes it already has rather than waiting.
func (srv *server) getCanvasProviderClasses(facilityID *uint) (classes []ProgramClassCohort, loading bool) {
	kv := srv.buckets[CanvasClasses]
	if kv == nil {
		log.Warn("canvas_classes NATS bucket is nil, skipping cache")
		return nil, false
	}
	cacheKey := canvasClassCacheKey(facilityID)

	if entry, err := kv.Get(cacheKey); err == nil {
		var cached CachedCanvasClasses
		if json.Unmarshal(entry.Value(), &cached) == nil {
			if cached.Loading {
				// A refresh is already running; show what it had last, if anything.
				return cached.Classes, true
			}
			if cached.LastUpdated.Add(canvasClassCacheTTL).After(time.Now()) {
				return cached.Classes, false
			}
			// Stale: serve the old classes while the refresh runs, so the list does
			// not empty out every five minutes.
			srv.warmCanvasClassCache(facilityID, cacheKey, cached.Classes)
			return cached.Classes, true
		}
	}

	srv.warmCanvasClassCache(facilityID, cacheKey, nil)
	return nil, true
}

// warmCanvasClassCache refreshes a cached class listing in the background.
// previous is carried into the loading marker so a stale-but-usable list keeps
// being served while the refresh runs.
func (srv *server) warmCanvasClassCache(facilityID *uint, cacheKey string, previous []ProgramClassCohort) {
	if _, loaded := srv.canvasInflight.LoadOrStore(cacheKey, struct{}{}); loaded {
		return
	}
	kv := srv.buckets[CanvasClasses]
	if kv == nil {
		srv.canvasInflight.Delete(cacheKey)
		return
	}
	srv.putCanvasClasses(cacheKey, CachedCanvasClasses{Classes: previous, LastUpdated: time.Now(), Loading: true})

	var scoped *uint
	if facilityID != nil {
		id := *facilityID
		scoped = &id
	}
	go func() {
		defer srv.canvasInflight.Delete(cacheKey)
		var (
			classes []ProgramClassCohort
			failed  []uint
			err     error
		)
		if scoped == nil {
			classes, failed, err = srv.fetchCanvasClassesAllProvidersAllFacilities()
		} else {
			classes, failed, err = srv.fetchCanvasClassesAllProviders(scoped)
		}
		if err != nil {
			log.WithError(err).Warn("warmCanvasClassCache: failed to fetch provider classes")
			// Clear the loading marker, and date the entry so the next request
			// refreshes it. Writing time.Now() here would serve this failed result
			// as fresh for a full TTL instead.
			srv.restoreCanvasClasses(cacheKey, previous)
			return
		}
		if len(failed) > 0 {
			// A partial listing must not be stored as fresh: the providers that
			// failed contributed nothing, so their classes would vanish from the
			// page for a whole TTL. Keep serving what they had and retry sooner.
			log.Warnf("warmCanvasClassCache: %d provider(s) failed, carrying over their cached classes", len(failed))
			classes = carryOverFailedProviders(classes, previous, failed,
				func(c ProgramClassCohort) uint { return c.ProgramID })
			if len(classes) == 0 {
				srv.restoreCanvasClasses(cacheKey, previous)
				return
			}
			srv.putCanvasClasses(cacheKey, CachedCanvasClasses{
				Classes:     classes,
				LastUpdated: retryableCacheTimestamp(canvasClassCacheTTL),
			})
			return
		}
		srv.putCanvasClasses(cacheKey, CachedCanvasClasses{Classes: classes, LastUpdated: time.Now(), Loading: false})
	}()
}

// restoreCanvasClasses puts a failed refresh back into a retryable state: the
// stale listing it was replacing, dated so the next request refreshes it, or no
// entry at all when there is nothing worth serving.
func (srv *server) restoreCanvasClasses(cacheKey string, previous []ProgramClassCohort) {
	if previous == nil {
		if kv := srv.buckets[CanvasClasses]; kv != nil {
			if err := kv.Delete(cacheKey); err != nil {
				log.WithError(err).Warnf("restoreCanvasClasses: failed to clear loading marker at %s", cacheKey)
			}
		}
		return
	}
	srv.putCanvasClasses(cacheKey, CachedCanvasClasses{
		Classes:     previous,
		LastUpdated: retryableCacheTimestamp(canvasClassCacheTTL),
	})
}

func (srv *server) putCanvasClasses(cacheKey string, cached CachedCanvasClasses) {
	kv := srv.buckets[CanvasClasses]
	if kv == nil {
		return
	}
	data, err := json.Marshal(cached)
	if err != nil {
		log.WithError(err).Warn("warmCanvasClassCache: failed to encode class cache entry")
		return
	}
	if _, err := kv.Put(cacheKey, data); err != nil {
		log.WithError(err).Warn("warmCanvasClassCache: failed to write class cache entry")
	}
}

// loadingClassPlaceholders returns one row per live provider so the classes page
// can show that a provider is still syncing. They carry Loading so the frontend
// can keep them out of the filtered list and render them as skeleton rows.
func (srv *server) loadingClassPlaceholders(facilityID *uint) []ProgramClassCohort {
	providers, err := srv.activeProviders(context.Background())
	if err != nil {
		log.WithError(err).Warn("loadingClassPlaceholders: failed to list provider platforms")
		return nil
	}
	var scopedFacility uint
	if facilityID != nil {
		scopedFacility = *facilityID
	}
	var placeholders []ProgramClassCohort
	for _, provider := range providers {
		if !isLiveProgramProvider(&provider) {
			continue
		}
		cls := ProgramClassCohort{
			ProgramID:  CanvasProgramIDOffset + provider.ID,
			FacilityID: scopedFacility,
			ClassName:  provider.Name,
			Status:     Active,
			IsCanvas:   true,
			Source:     providerSourceLabel(&provider),
			Loading:    true,
		}
		cls.ID = CanvasProgramIDOffset + provider.ID
		placeholders = append(placeholders, cls)
	}
	return placeholders
}
