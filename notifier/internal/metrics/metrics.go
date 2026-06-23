package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	NotifierJobsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "notifier_jobs_processed_total",
		Help: "Total notifier job outcomes (sent, retried, failed, duplicate).",
	}, []string{"outcome"})

	NotifierJobDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "notifier_job_duration_seconds",
		Help:    "Duration of notifier job processing in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	ConfirmationEmails = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "notifier_confirmation_emails_total",
		Help: "Total confirmation-email outcomes by transport (rest, grpc) and outcome (sent, failed).",
	}, []string{"transport", "outcome"})
)

// init seeds each series at zero. A CounterVec series otherwise first appears
// already at 1, so rate() never sees the increment that created it.
func init() {
	for _, outcome := range []string{"sent", "retried", "failed", "duplicate"} {
		NotifierJobsProcessed.WithLabelValues(outcome)
	}
	for _, transport := range []string{"rest", "grpc"} {
		for _, outcome := range []string{"sent", "failed"} {
			ConfirmationEmails.WithLabelValues(transport, outcome)
		}
	}
}
