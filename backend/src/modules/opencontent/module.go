// Package opencontent is the Open Content feature module: libraries (Kiwix),
// videos, helpful links and the content proxy. Its code moved here from core
// in EN-192.
package opencontent

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

//go:embed migrations
var migrations embed.FS

// Module is the Open Content module.
type Module struct{}

var (
	_ module.Module     = Module{}
	_ module.Schema     = Module{}
	_ module.Seeder     = Module{}
	_ module.TestModels = Module{}
	_ module.Worker     = Module{}
)

func (Module) Name() string { return "opencontent" }

// Schema is the Postgres schema that holds Open Content's tables.
func (Module) Schema() string { return "open_content" }

// Register adds Open Content's routes. A failure to reach AWS or NATS stops
// the server, as it did when core set them up.
func (Module) Register(r module.Router, deps module.Deps) {
	srv := &server{
		store:  &store{db: deps.DB},
		notify: deps.Notify,
		nats:   deps.NATS,
		client: &http.Client{},
		dev:    os.Getenv("APP_ENV") == "dev",
	}
	if err := srv.connectAWS(context.Background()); err != nil {
		log.Fatal(err)
	}
	if deps.NATS != nil {
		kv, err := libraryPathsBucket(deps.NATS)
		if err != nil {
			log.Fatalf("Failed to setup JetStream KV store: %v", err)
		}
		srv.libraryPaths = kv
	}
	for _, rt := range srv.routes() {
		r.Handle(rt)
	}
}

// RegisterJobs schedules the Kiwix scrape and the video jobs for the worker,
// and runs each once when the scheduler starts. In dev only the scrape runs
// at start.
func (Module) RegisterJobs(s module.Scheduler, deps module.Deps) {
	p := &jobPublisher{store: &store{db: deps.DB}, publish: deps.NATS.PublishMsg}
	scheduleJobs(s, p, os.Getenv("APP_ENV") == "dev")
}

// Work runs Open Content's jobs until ctx is canceled (worker.go).
func (Module) Work(ctx context.Context, deps module.Deps) error {
	w := &worker{store: &store{db: deps.DB}}
	return w.run(ctx, deps.NATS)
}

func (Module) Migrations() fs.FS { return migrations }

// connectAWS sets up email for content requests and, when S3_BUCKET_NAME is
// set, the presigned video links.
func (srv *server) connectAWS(ctx context.Context) error {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	srv.sesClient = sesv2.NewFromConfig(cfg)
	if bucket := os.Getenv("S3_BUCKET_NAME"); bucket != "" {
		srv.presigner = s3.NewPresignClient(s3.NewFromConfig(cfg))
		srv.s3Bucket = bucket
	}
	return nil
}

// libraryPaths is the NATS key-value bucket that caches each library's proxy
// target.
const libraryPaths = "library_paths"

// libraryPathsBucket opens the library_paths bucket, creating it on first
// start.
func libraryPathsBucket(nc *nats.Conn) (nats.KeyValue, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("initialize JetStream: %w", err)
	}
	kv, err := js.KeyValue(libraryPaths)
	if err == nil {
		return kv, nil
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{Bucket: libraryPaths, History: 1, TTL: 24 * time.Hour})
}

// defaultLeftMenuLinks are the helpful links a new database starts with.
// Provider 1 is the HelpfulLinks provider that migration 00026 creates.
const defaultLeftMenuLinks = `[{
			"title": "Unlocked Labs",
			"description": "Unlocked Labs website",
			"url": "https://unlockedlabs.org",
			"visibility_status": true,
	        "thumbnail_url": "https://unlockedlabs.org/favicon.ico",
			"open_content_provider_id": 1,
			"facility_id": 1
		},
	    {
	    "title": "Google",
	    "description": "Google search engine",
	    "url": "https://www.google.com",
	    "visibility_status": true,
	    "open_content_provider_id": 1,
	    "thumbnail_url": "https://www.google.com/favicon.ico",
	    "facility_id": 1
	    }]`

// Seed adds the default helpful links, visible at facilityID, and the Kiwix
// and YouTube providers.
func (Module) Seed(ctx context.Context, db *gorm.DB, facilityID uint) error {
	db = db.WithContext(ctx)
	links := []HelpfulLink{}
	if err := json.Unmarshal([]byte(defaultLeftMenuLinks), &links); err != nil {
		return fmt.Errorf("read default left menu links: %w", err)
	}
	if err := db.Create(&links).Error; err != nil {
		return fmt.Errorf("create left menu links: %w", err)
	}
	for _, link := range links {
		if link.OpenContentProviderID == 0 {
			continue
		}
		visibility := FacilityVisibilityStatus{
			FacilityID:            facilityID,
			OpenContentProviderID: link.OpenContentProviderID,
			ContentID:             link.ID,
			VisibilityStatus:      true,
		}
		if err := db.Create(&visibility).Error; err != nil {
			return fmt.Errorf("create link visibility: %w", err)
		}
	}
	providers := []OpenContentProvider{
		{Title: stringPtr(Kiwix), Url: KiwixLibraryUrl, CurrentlyEnabled: true, ThumbnailUrl: stringPtr(KiwixThumbnailURL), Description: stringPtr(Kiwix)},
		{Title: stringPtr(Youtube), Url: YoutubeApi, CurrentlyEnabled: true, ThumbnailUrl: stringPtr(YoutubeThumbnail), Description: stringPtr(YoutubeDescription)},
	}
	for i := range providers {
		if err := db.Create(&providers[i]).Error; err != nil {
			return fmt.Errorf("create default open content providers: %w", err)
		}
	}
	return nil
}

// TestModels is Open Content's tables, for core's SQLite test database.
func (Module) TestModels() []any {
	return []any{
		&HelpfulLink{},
		&OpenContentProvider{},
		&OpenContentUrl{},
		&OpenContentActivity{},
		&Library{},
		&FacilityVisibilityStatus{},
		&Video{},
		&VideoDownloadAttempt{},
		&OpenContentFavorite{},
		&Tag{},
		&OpenContentTag{},
	}
}
