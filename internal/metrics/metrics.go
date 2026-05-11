package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests by method, path, and status.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	GitHubAPIRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "github_api_requests_total",
		Help: "Total GitHub API requests by HTTP status code.",
	}, []string{"status"})

	NotificationsEnqueued = promauto.NewCounter(prometheus.CounterOpts{
		Name: "notifications_enqueued_total",
		Help: "Total notifications enqueued for delivery.",
	})

	NotificationsSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "notifications_sent_total",
		Help: "Total notifications successfully sent.",
	})

	NotificationsFailed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "notifications_failed_total",
		Help: "Total notifications that failed to send after all retries.",
	})

	ConfirmationEmailsSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "confirmation_emails_sent_total",
		Help: "Total subscription confirmation emails successfully sent.",
	})

	ScannerRuns = promauto.NewCounter(prometheus.CounterOpts{
		Name: "scanner_runs_total",
		Help: "Total scanner scan cycles.",
	})

	ScannerDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "scanner_duration_seconds",
		Help:    "Duration of scanner scan cycles.",
		Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
	})

	ActiveSubscriptions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "active_subscriptions",
		Help: "Current number of confirmed subscriptions.",
	})
)
