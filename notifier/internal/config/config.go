package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	MetricsPort int    `env:"METRICS_PORT" envDefault:"8081"`
	BaseURL     string `env:"BASE_URL" envDefault:"http://localhost:8080"`

	RedisURL string `env:"REDIS_URL" envDefault:"redis://localhost:6379/0"`

	MailgunDomain  string `env:"MAILGUN_DOMAIN"`
	MailgunAPIKey  string `env:"MAILGUN_API_KEY"`
	MailgunFrom    string `env:"MAILGUN_FROM" envDefault:"noreply@releases.app"`
	MailgunAPIBase string `env:"MAILGUN_API_BASE"` // e.g. https://api.eu.mailgun.net/v3

	NotificationWorkers int `env:"NOTIFICATION_WORKERS" envDefault:"10"`

	JaegerEndpoint string `env:"JAEGER_ENDPOINT"`
	OTelEnabled    bool   `env:"OTEL_ENABLED" envDefault:"false"`

	Debug bool `env:"DEBUG" envDefault:"false"`
}

func (c *Config) UseConsoleEmail() bool {
	return c.Debug || c.MailgunDomain == "" || c.MailgunAPIKey == ""
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}
