package main

import (
	"context"
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
	"github-release-notifier/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logLevel := slog.LevelInfo
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.Debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	// --- Tracing (must init before DB so otelsql picks up the real TracerProvider) ---
	if cfg.OTelEnabled && cfg.JaegerEndpoint != "" {
		shutdown, err := tracing.Init(context.Background(), cfg.JaegerEndpoint)
		if err != nil {
			slog.Warn("tracing init failed, continuing without traces", "error", err)
		} else {
			defer func() { _ = shutdown(context.Background()) }()
			slog.Info("tracing enabled", "endpoint", cfg.JaegerEndpoint)
		}
	}

	// --- Database ---
	sqlDB, err := otelsql.Open("postgres", cfg.DatabaseURL,
		otelsql.WithAttributes(semconv.DBSystemPostgreSQL),
		otelsql.WithSpanOptions(otelsql.SpanOptions{Ping: true}),
	)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	db := sqlx.NewDb(sqlDB, "postgres")
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(18) // Heroku allows 20; leave headroom for migrations and admin.
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		return err
	}

	// --- Redis ---
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parsing redis URL: %w", err)
	}
	// Heroku Redis uses self-signed certificates; skip verification when TLS is enabled.
	if redisOpts.TLSConfig != nil {
		redisOpts.TLSConfig.InsecureSkipVerify = true
	}
	redisOpts.PoolSize = 15
	redisOpts.MinIdleConns = 5
	rdb := redis.NewClient(redisOpts)
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return fmt.Errorf("connecting to redis: %w", err)
	}

	// --- Dependencies ---
	repoStore := postgres.NewRepositoryStore(db)
	subStore := postgres.NewSubscriptionStore(db)

	gh := ghclient.NewClient(cfg.GitHubToken)
	cachedGH := cache.NewCachedGitHubClient(gh, rdb)

	// --- Email ---
	var mailer service.EmailSender
	if cfg.UseConsoleEmail() {
		slog.Info("using console email backend (emails logged to stdout)")
		mailer = email.NewLogSender()
	} else {
		slog.Info("using Mailgun email backend", "domain", cfg.MailgunDomain)
		mailer = email.NewSender(cfg.MailgunDomain, cfg.MailgunAPIKey, cfg.MailgunFrom, cfg.MailgunAPIBase)
	}

	notifQueue := queue.NewNotificationQueue(rdb)

	subscriptionSvc := service.NewSubscriptionService(subStore, repoStore, cachedGH, mailer, cfg.BaseURL)
	scanner := service.NewScanner(repoStore, subStore, cachedGH, notifQueue, cfg.BaseURL, cfg.ScanInterval, cfg.ScanWorkers)
	notifier := service.NewNotifier(notifQueue, mailer, cfg.BaseURL, cfg.NotificationWorkers)
	cleanup := service.NewCleanup(subStore)

	// --- HTTP Server ---
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
		api.POST("/subscribe", handler.Subscribe(subscriptionSvc))
		api.GET("/confirm/:token", handler.Confirm(subscriptionSvc))
		api.GET("/unsubscribe/:token", handler.Unsubscribe(subscriptionSvc))
		api.GET("/subscriptions", handler.Subscriptions(subscriptionSvc))
	}

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// --- gRPC Server ---
	grpcSrv := grpc.NewServer()
	pb.RegisterSubscriptionServiceServer(grpcSrv, grpcserver.NewServer(subscriptionSvc))

	// --- Start ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scanner.Run(ctx)
	go notifier.Run(ctx)
	go cleanup.Run(ctx)

	go func() {
		lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
		if err != nil {
			slog.Error("gRPC listen failed", "error", err)
			return
		}
		slog.Info("gRPC server started", "port", cfg.GRPCPort)
		if err := grpcSrv.Serve(lis); err != nil {
			slog.Error("gRPC serve failed", "error", err)
		}
	}()

	go func() {
		slog.Info("HTTP server started", "port", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
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

func runMigrations(dbURL string) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("creating migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		return fmt.Errorf("creating migrator: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("running migrations: %w", err)
	}
	slog.Info("database migrations applied")
	return nil
}
