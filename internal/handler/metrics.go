package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests by method, path, and status.",
	}, []string{"method", "path", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

// PrometheusMiddleware collects HTTP request metrics.
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()

		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}

		httpRequestsTotal.WithLabelValues(
			c.Request.Method,
			path,
			strconv.Itoa(c.Writer.Status()),
		).Inc()

		httpRequestDuration.WithLabelValues(
			c.Request.Method,
			path,
		).Observe(duration)
	}
}

// Application-level metrics exposed for use by other packages.
var (
	GitHubAPIRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "github_api_requests_total",
		Help: "Total GitHub API requests by status.",
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
		Help: "Total notifications that failed to send.",
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
