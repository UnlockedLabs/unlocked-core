package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// The admin calendar used to fetch a live provider's events on the request
// path, so the whole calendar waited on Canvas and Essential Ed before showing
// the events we already have in our own tables. Instead the provider events are
// cached in NATS KV and refreshed in the background, exactly as
// canvas_classes_cache.go does for the classes page.

// canvasEventCacheTTL is how long a cached event listing is served before it is
// refreshed. It matches the bucket TTL.
const canvasEventCacheTTL = 5 * time.Minute

type CachedCanvasEvents struct {
	Events      []FacilityProgramClassEvent
	LastUpdated time.Time
	Loading     bool
}

// canvasEventCacheKey scopes the cache to the facility and the visible date
// range. The calendar pages week to week, so each range is its own entry; day
// granularity is enough, and keeps a few seconds of drift in the requested
// range from fragmenting the cache.
func canvasEventCacheKey(facilityID uint, dtRng *DateRange) string {
	return fmt.Sprintf("canvas_events_%d_%s_%s", facilityID,
		dtRng.Start.Format("2006-01-02"), dtRng.End.Format("2006-01-02"))
}

// getCanvasProviderEvents returns the cached provider events for this facility
// and date range. A fresh entry is returned as-is. On a miss or a stale entry it
// returns loading=true and refreshes in the background, so the caller can answer
// with the events it already has rather than waiting.
func (srv *server) getCanvasProviderEvents(dtRng *DateRange, facilityID uint) (events []FacilityProgramClassEvent, loading bool) {
	kv := srv.buckets[CanvasEvents]
	if kv == nil {
		log.Warn("canvas_events NATS bucket is nil, skipping cache")
		return nil, false
	}
	cacheKey := canvasEventCacheKey(facilityID, dtRng)

	if entry, err := kv.Get(cacheKey); err == nil {
		var cached CachedCanvasEvents
		if json.Unmarshal(entry.Value(), &cached) == nil {
			if cached.Loading {
				// A refresh is already running; show what it had last, if anything.
				return cached.Events, true
			}
			if cached.LastUpdated.Add(canvasEventCacheTTL).After(time.Now()) {
				return cached.Events, false
			}
			// Stale: serve the old events while the refresh runs, so the calendar
			// does not empty out every five minutes.
			srv.warmCanvasEventCache(dtRng, facilityID, cacheKey, cached.Events)
			return cached.Events, true
		}
	}

	srv.warmCanvasEventCache(dtRng, facilityID, cacheKey, nil)
	return nil, true
}

// warmCanvasEventCache refreshes a cached event listing in the background.
// previous is carried into the loading marker so a stale-but-usable list keeps
// being served while the refresh runs.
func (srv *server) warmCanvasEventCache(dtRng *DateRange, facilityID uint, cacheKey string, previous []FacilityProgramClassEvent) {
	if _, loaded := srv.canvasInflight.LoadOrStore(cacheKey, struct{}{}); loaded {
		return
	}
	kv := srv.buckets[CanvasEvents]
	if kv == nil {
		srv.canvasInflight.Delete(cacheKey)
		return
	}
	srv.putCanvasEvents(cacheKey, CachedCanvasEvents{Events: previous, LastUpdated: time.Now(), Loading: true})

	scoped := *dtRng
	go func() {
		defer srv.canvasInflight.Delete(cacheKey)
		events, failed, err := srv.appendCanvasEventsForFacility(&scoped, facilityID)
		if err != nil {
			log.WithError(err).Warn("warmCanvasEventCache: failed to fetch provider events")
			// Clear the loading marker, and date the entry so the next request
			// refreshes it. Writing time.Now() here would serve this failed result
			// as fresh for a full TTL instead.
			srv.restoreCanvasEvents(cacheKey, previous)
			return
		}
		if len(failed) > 0 {
			// A partial listing must not be stored as fresh: the providers that
			// failed contributed nothing, so their events would vanish from the
			// calendar for a whole TTL. Keep serving what they had and retry sooner.
			log.Warnf("warmCanvasEventCache: %d provider(s) failed, carrying over their cached events", len(failed))
			events = carryOverFailedProviders(events, previous, failed,
				func(e FacilityProgramClassEvent) uint { return e.ProgramID })
			if len(events) == 0 {
				srv.restoreCanvasEvents(cacheKey, previous)
				return
			}
			srv.putCanvasEvents(cacheKey, CachedCanvasEvents{
				Events:      events,
				LastUpdated: retryableCacheTimestamp(canvasEventCacheTTL),
			})
			return
		}
		srv.putCanvasEvents(cacheKey, CachedCanvasEvents{Events: events, LastUpdated: time.Now(), Loading: false})
	}()
}

// restoreCanvasEvents puts a failed refresh back into a retryable state: the
// stale listing it was replacing, dated so the next request refreshes it, or no
// entry at all when there is nothing worth serving.
func (srv *server) restoreCanvasEvents(cacheKey string, previous []FacilityProgramClassEvent) {
	if previous == nil {
		if kv := srv.buckets[CanvasEvents]; kv != nil {
			if err := kv.Delete(cacheKey); err != nil {
				log.WithError(err).Warnf("restoreCanvasEvents: failed to clear loading marker at %s", cacheKey)
			}
		}
		return
	}
	srv.putCanvasEvents(cacheKey, CachedCanvasEvents{
		Events:      previous,
		LastUpdated: retryableCacheTimestamp(canvasEventCacheTTL),
	})
}

func (srv *server) putCanvasEvents(cacheKey string, cached CachedCanvasEvents) {
	kv := srv.buckets[CanvasEvents]
	if kv == nil {
		return
	}
	data, err := json.Marshal(cached)
	if err != nil {
		log.WithError(err).Warn("warmCanvasEventCache: failed to encode event cache entry")
		return
	}
	if _, err := kv.Put(cacheKey, data); err != nil {
		log.WithError(err).Warn("warmCanvasEventCache: failed to write event cache entry")
	}
}

// loadingEventPlaceholders returns one marker per live provider so the calendar
// can show which provider is still syncing. They carry Loading and no times, so
// the frontend keeps them out of the grid and renders them as an indicator.
func (srv *server) loadingEventPlaceholders() []FacilityProgramClassEvent {
	providers, err := srv.activeProviders(context.Background())
	if err != nil {
		log.WithError(err).Warn("loadingEventPlaceholders: failed to list provider platforms")
		return nil
	}
	var placeholders []FacilityProgramClassEvent
	for _, provider := range providers {
		if !isLiveProgramProvider(&provider) {
			continue
		}
		ev := FacilityProgramClassEvent{
			ProgramID:     CanvasProgramIDOffset + provider.ID,
			ProgramName:   provider.Name,
			IsCanvasEvent: true,
			Source:        providerSourceLabel(&provider),
			Loading:       true,
		}
		ev.ID = CanvasProgramIDOffset + provider.ID
		placeholders = append(placeholders, ev)
	}
	return placeholders
}
