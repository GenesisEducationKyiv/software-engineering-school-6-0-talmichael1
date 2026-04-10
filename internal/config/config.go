package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds all application configuration parsed from environment variables.
type Config struct {
	Port     int    `env:"PORT" envDefault:"8080"`
	GRPCPort int    `env:"GRPC_PORT" envDefault:"9090"`
	BaseURL  string `env:"BASE_URL" envDefault:"http://localhost:8080"`

	DatabaseURL string `env:"DATABASE_URL,required"`
	RedisURL    string `env:"REDIS_URL" envDefault:"redis://localhost:6379/0"`

	GitHubToken string `env:"GITHUB_TOKEN"`

	MailgunDomain  string `env:"MAILGUN_DOMAIN"`
	MailgunAPIKey  string `env:"MAILGUN_API_KEY"`
	MailgunFrom    string `env:"MAILGUN_FROM" envDefault:"noreply@releases.app"`
	MailgunAPIBase string `env:"MAILGUN_API_BASE"` // e.g. https://api.eu.mailgun.net/v3

	ScanInterval        time.Duration `env:"SCAN_INTERVAL" envDefault:"5m"`
	NotificationWorkers int           `env:"NOTIFICATION_WORKERS" envDefault:"10"`

	APIKey string `env:"API_KEY"`

	JaegerEndpoint string `env:"JAEGER_ENDPOINT"`
	OTelEnabled    bool   `env:"OTEL_ENABLED" envDefault:"false"`

	Debug       bool   `env:"DEBUG" envDefault:"false"`
	CORSOrigins string `env:"CORS_ORIGINS" envDefault:"*"`
}

// UseConsoleEmail returns true when Mailgun is not configured or debug mode is on.
func (c *Config) UseConsoleEmail() bool {
	return c.Debug || c.MailgunDomain == "" || c.MailgunAPIKey == ""
}

// Load parses configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}
