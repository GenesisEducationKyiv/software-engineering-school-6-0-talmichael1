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

	GitHubClientRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "github_client_requests_total",
		Help: "Total GitHub API requests by operation and HTTP status code.",
	}, []string{"operation", "status"})

	GitHubClientDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "github_client_request_duration_seconds",
		Help:    "Duration of GitHub API operations in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation"})

	NotificationsEnqueued = promauto.NewCounter(prometheus.CounterOpts{
		Name: "notifications_enqueued_total",
		Help: "Total notifications enqueued for delivery.",
	})

	NotifierJobsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "notifier_jobs_processed_total",
		Help: "Total notifier job outcomes (sent, retried, failed, duplicate).",
	}, []string{"outcome"})

	NotifierJobDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "notifier_job_duration_seconds",
		Help:    "Duration of notifier job processing in seconds.",
		Buckets: prometheus.DefBuckets,
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

	ScannerErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scanner_errors_total",
		Help: "Total scanner errors by stage (list_repos, enqueue, check_repo, dequeue).",
	}, []string{"stage"})

	ActiveSubscriptions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "active_subscriptions",
		Help: "Current number of confirmed subscriptions.",
	})
)
