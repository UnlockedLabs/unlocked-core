package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/joho/godotenv"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

/**
* Handler struct that will be passed to our HTTP server handlers
* to handle the different routes.
* It will have a refernce to the KolibriService struct
**/
type ServiceHandler struct {
	nats   *nats.Conn
	Mux    *http.ServeMux
	token  string
	db     *gorm.DB
	cancel context.CancelFunc
	ctx    context.Context
}

var logger = sync.OnceValue(func() *log.Logger { return initLogging() })

func newServiceHandler(token string, db *gorm.DB) *ServiceHandler {
	options := nats.GetDefaultOptions()
	options.Url = os.Getenv("NATS_URL")
	options.User = os.Getenv("NATS_USER")
	options.Password = os.Getenv("NATS_PASSWORD")
	if options.Url == "" {
		options.Url = "nats://nats:4222"
	}
	conn, err := options.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	log.Println("Connected to NATS at ", options.Url)
	ctx, cancel := context.WithCancel(context.Background())
	return &ServiceHandler{
		token:  token,
		db:     db,
		nats:   conn,
		Mux:    http.NewServeMux(),
		cancel: cancel,
		ctx:    ctx,
	}
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Failed to load .env file, using default env variables")
	}
	dsn := os.Getenv("APP_DSN")
	if dsn == "" {
		dsn = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=prefer",
			os.Getenv("DB_HOST"), os.Getenv("DB_PORT"), os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_NAME"))
	}
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN: dsn,
	}), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL database: %v", err)
	}

	token := os.Getenv("PROVIDER_SERVICE_KEY")
	handler := newServiceHandler(token, db)
	log.Println("Server started on :8081")
	handler.registerRoutes()
	err = handler.initSubscription()
	if err != nil {
		handler.cancel()
		logger().Fatalf("Failed to subscribe to NATS: %v", err)
	}
	err = http.ListenAndServe(":8081", handler.Mux)
	if err != nil {
		handler.cancel()
		logger().Fatalf("Failed to start server: %v", err)
	}
}

func initLogging() *log.Logger {
	var logger = log.New()
	logger.SetFormatter(&log.JSONFormatter{})
	logger.SetLevel(log.InfoLevel)
	return logger
}
