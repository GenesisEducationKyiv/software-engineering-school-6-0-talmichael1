package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Port     int    `env:"PORT" envDefault:"8080"`
	GRPCPort int    `env:"GRPC_PORT" envDefault:"9090"`
	BaseURL  string `env:"BASE_URL" envDefault:"http://localhost:8080"`

	DatabaseURL string `env:"DATABASE_URL,required"`
	RedisURL    string `env:"REDIS_URL" envDefault:"redis://localhost:6379/0"`
	RabbitURL   string `env:"RABBITMQ_URL" envDefault:"amqp://guest:guest@localhost:5672/"`

	GitHubToken string `env:"GITHUB_TOKEN"`

	// NotifierURL is the base URL of the Notifier service's internal HTTP API,
	// called by the subscribe saga to send confirmation emails (ADR-0007).
	NotifierURL string `env:"NOTIFIER_URL" envDefault:"http://localhost:8082"`

	ScanInterval time.Duration `env:"SCAN_INTERVAL" envDefault:"5m"`
	ScanWorkers  int           `env:"SCAN_WORKERS" envDefault:"5"`

	APIKey string `env:"API_KEY"`

	JaegerEndpoint string `env:"JAEGER_ENDPOINT"`
	OTelEnabled    bool   `env:"OTEL_ENABLED" envDefault:"false"`

	Debug       bool   `env:"DEBUG" envDefault:"false"`
	CORSOrigins string `env:"CORS_ORIGINS" envDefault:"*"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}
