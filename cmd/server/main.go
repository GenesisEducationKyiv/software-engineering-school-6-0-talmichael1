package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/XSAM/otelsql"
	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"google.golang.org/grpc"

	"github-release-notifier/internal/cache"
	"github-release-notifier/internal/config"
	"github-release-notifier/internal/email"
	ghclient "github-release-notifier/internal/github"
	grpcserver "github-release-notifier/internal/grpc"
	pb "github-release-notifier/internal/grpc/proto"
	"github-release-notifier/internal/handler"
	"github-release-notifier/internal/queue"
	"github-release-notifier/internal/repository/postgres"
	"github-release-notifier/internal/service"
	"github-release-notifier/internal/tracing"
	"github-release-notifier/internal/urls"
	"github-release-notifier/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	configureLogger(cfg)

	if cfg.OTelEnabled && cfg.JaegerEndpoint != "" {
		shutdown, err := tracing.Init(context.Background(), cfg.JaegerEndpoint)
		if err != nil {
			slog.Warn("tracing init failed, continuing without traces", "error", err)
		} else {
			defer func() { _ = shutdown(context.Background()) }()
			slog.Info("tracing enabled", "endpoint", cfg.JaegerEndpoint)
		}
	}

	db, err := connectDB(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		return err
	}

	rdb, err := connectRedis(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	subscriptionSvc, scanner, notifier, cleanup := buildServices(cfg, db, rdb)

	router := buildRouter(cfg, subscriptionSvc)
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	grpcSrv := newGRPCServer(subscriptionSvc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scanner.Run(ctx)
	go notifier.Run(ctx)
	go cleanup.Run(ctx)

	errCh := make(chan error, 2)
	go func() { errCh <- serveGRPC(grpcSrv, cfg.GRPCPort) }()
	go func() { errCh <- serveHTTP(httpServer, cfg.Port) }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		slog.Info("shutting down...")
	case err := <-errCh:
		slog.Error("server failed, shutting down", "error", err)
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	grpcSrv.GracefulStop()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
	return nil
}

func configureLogger(cfg *config.Config) {
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}

func connectDB(dsn string) (*sqlx.DB, error) {
	sqlDB, err := otelsql.Open("postgres", dsn,
		otelsql.WithAttributes(semconv.DBSystemPostgreSQL),
		otelsql.WithSpanOptions(otelsql.SpanOptions{Ping: true}),
	)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	db := sqlx.NewDb(sqlDB, "postgres")
	db.SetMaxOpenConns(18) // Heroku allows 20; leave headroom for migrations and admin.
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, nil
}

func connectRedis(rawURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing redis URL: %w", err)
	}
	// Heroku Redis uses self-signed certificates; skip verification when TLS is enabled.
	if opts.TLSConfig != nil {
		opts.TLSConfig.InsecureSkipVerify = true
	}
	opts.PoolSize = 15
	opts.MinIdleConns = 5
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("connecting to redis: %w", err)
	}
	return rdb, nil
}

func buildServices(cfg *config.Config, db *sqlx.DB, rdb *redis.Client) (
	*service.SubscriptionService, *service.Scanner, *service.Notifier, *service.Cleanup,
) {
	repoStore := postgres.NewRepositoryStore(db)
	subStore := postgres.NewSubscriptionStore(db)

	gh := ghclient.NewClient(cfg.GitHubToken)
	cachedGH := cache.NewCachedGitHubClient(gh, rdb)

	var mailer email.Sender
	if cfg.UseConsoleEmail() {
		slog.Info("using console email backend (emails logged to stdout)")
		mailer = email.NewLogSender()
	} else {
		slog.Info("using Mailgun email backend", "domain", cfg.MailgunDomain)
		mailer = email.NewMailgunSender(cfg.MailgunDomain, cfg.MailgunAPIKey, cfg.MailgunFrom, cfg.MailgunAPIBase)
	}

	notifQueue := queue.NewNotificationQueue(rdb)
	urlBuilder := urls.Builder{BaseURL: cfg.BaseURL}

	subscriptionSvc := service.NewSubscriptionService(subStore, repoStore, cachedGH, mailer, urlBuilder)
	scanner := service.NewScanner(repoStore, subStore, cachedGH, notifQueue, cfg.ScanInterval, cfg.ScanWorkers)
	notifier := service.NewNotifier(notifQueue, mailer, urlBuilder, cfg.NotificationWorkers)
	cleanup := service.NewCleanup(subStore)
	return subscriptionSvc, scanner, notifier, cleanup
}

func buildRouter(cfg *config.Config, svc *service.SubscriptionService) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(handler.CORS(cfg.CORSOrigins))
	if cfg.OTelEnabled {
		router.Use(otelgin.Middleware("github-release-notifier"))
	}
	router.Use(handler.RequestLogger())
	router.Use(handler.PrometheusMiddleware())

	router.GET("/health", handler.Health())
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	api := router.Group("/api")
	api.Use(handler.APIKeyAuth(cfg.APIKey))
	{
		api.POST("/subscribe", handler.Subscribe(svc))
		api.GET("/confirm/:token", handler.Confirm(svc))
		api.GET("/unsubscribe/:token", handler.Unsubscribe(svc))
		api.GET("/subscriptions", handler.Subscriptions(svc))
	}
	return router
}

func newGRPCServer(svc *service.SubscriptionService) *grpc.Server {
	g := grpc.NewServer()
	pb.RegisterSubscriptionServiceServer(g, grpcserver.NewServer(svc))
	return g
}

func serveGRPC(g *grpc.Server, port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("gRPC listen: %w", err)
	}
	slog.Info("gRPC server started", "port", port)
	if err := g.Serve(lis); err != nil {
		return fmt.Errorf("gRPC serve: %w", err)
	}
	return nil
}

func serveHTTP(s *http.Server, port int) error {
	slog.Info("HTTP server started", "port", port)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP serve: %w", err)
	}
	return nil
}

func runMigrations(dbURL string) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("creating migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		return fmt.Errorf("creating migrator: %w", err)
	}
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			slog.Warn("closing migrator", "source_error", srcErr, "db_error", dbErr)
		}
	}()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("running migrations: %w", err)
	}
	slog.Info("database migrations applied")
	return nil
}
