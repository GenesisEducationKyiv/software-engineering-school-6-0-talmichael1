package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github-release-notifier/notifier/internal/config"
	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/logging"
	"github-release-notifier/notifier/internal/notifier"
	"github-release-notifier/notifier/internal/queue"
	"github-release-notifier/notifier/internal/tracing"
	"github-release-notifier/notifier/internal/urls"
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

	rdb, err := connectRedis(cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	worker := notifier.New(
		queue.NewNotificationQueue(rdb),
		buildMailer(cfg),
		urls.Builder{BaseURL: cfg.BaseURL},
		cfg.NotificationWorkers,
	)

	metricsServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler:      metricsHandler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { worker.Run(ctx) }()

	errCh := make(chan error, 1)
	go func() { errCh <- serveMetrics(metricsServer) }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		slog.Info("shutting down...")
	case err := <-errCh:
		slog.Error("metrics server failed, shutting down", "error", err)
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("metrics server shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
	return nil
}

func configureLogger(cfg *config.Config) {
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(logging.NewContextHandler(base)))
}

func buildMailer(cfg *config.Config) email.Sender {
	if cfg.UseConsoleEmail() {
		slog.Info("using console email backend (emails logged to stdout)")
		return email.NewLogSender()
	}
	slog.Info("using Mailgun email backend", "domain", cfg.MailgunDomain)
	return email.NewMailgunSender(cfg.MailgunDomain, cfg.MailgunAPIKey, cfg.MailgunFrom, cfg.MailgunAPIBase)
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

func metricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func serveMetrics(s *http.Server) error {
	slog.Info("metrics server started", "addr", s.Addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("metrics serve: %w", err)
	}
	return nil
}
