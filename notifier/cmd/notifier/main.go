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
	"github-release-notifier/notifier/internal/confirm"
	"github-release-notifier/notifier/internal/dedup"
	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/logging"
	"github-release-notifier/notifier/internal/notifier"
	"github-release-notifier/notifier/internal/queue"
	"github-release-notifier/notifier/internal/tracing"
	"github-release-notifier/notifier/internal/urls"
)

// consumerPrefetch caps in-flight unacked messages per worker channel. One keeps
// a crashed worker from sitting on a backlog the broker can't redeliver.
const consumerPrefetch = 1

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

	rabbitConn, err := queue.Dial(cfg.RabbitURL)
	if err != nil {
		return fmt.Errorf("connecting to rabbitmq: %w", err)
	}
	defer func() { _ = rabbitConn.Close() }()

	consume := func(ctx context.Context) (notifier.JobConsumer, error) {
		return rabbitConn.Consumer(ctx, consumerPrefetch)
	}
	mailer := buildMailer(cfg)
	worker := notifier.New(
		consume,
		dedup.NewRedis(rdb),
		mailer,
		urls.Builder{BaseURL: cfg.BaseURL},
		cfg.NotificationWorkers,
	)

	metricsServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler:      metricsHandler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
	internalServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.InternalPort),
		Handler:      internalHandler(mailer),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { worker.Run(ctx) }()

	errCh := make(chan error, 2)
	go func() { errCh <- serve("metrics", metricsServer) }()
	go func() { errCh <- serve("internal", internalServer) }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		slog.Info("shutting down...")
	case err := <-errCh:
		slog.Error("http server failed, shutting down", "error", err)
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	for name, srv := range map[string]*http.Server{"metrics": metricsServer, "internal": internalServer} {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("http server shutdown error", "server", name, "error", err)
		}
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

func internalHandler(mailer email.Sender) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/internal/confirmations", confirm.NewHandler(mailer))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func serve(name string, s *http.Server) error {
	slog.Info("http server started", "server", name, "addr", s.Addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s serve: %w", name, err)
	}
	return nil
}
