package notifier

import (
	"context"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github-release-notifier/notifier/internal/domain"
	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/metrics"
)

func histogramCount(t *testing.T) uint64 {
	t.Helper()
	var m dto.Metric
	if err := metrics.NotifierJobDuration.Write(&m); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

func metricsJob(id int64) domain.NotificationJob {
	return domain.NotificationJob{SubscriptionID: id, Email: "u@example.com", Repo: "x/y", Tag: "v1", UnsubToken: "t"}
}

func TestNotifier_Metrics_SentIncrementsSentOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("sent"))

	q := newMockQueue()
	n := newNotifier(q, &releaseEmailMock{})

	if err := n.processJob(context.Background(), q, deliver(metricsJob(100))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("sent"))
	if delta := after - before; delta != 1 {
		t.Fatalf("sent counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_DuplicateIncrementsDuplicateOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("duplicate"))

	q := newMockQueue()
	q.sent["101:v1"] = true
	n := newNotifier(q, &releaseEmailMock{})

	if err := n.processJob(context.Background(), q, deliver(metricsJob(101))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("duplicate"))
	if delta := after - before; delta != 1 {
		t.Fatalf("duplicate counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_RetryIncrementsRetriedOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("retried"))

	q := newMockQueue()
	failing := &releaseEmailMock{sendFn: func(_ context.Context, _ email.Message) error {
		return fmt.Errorf("smtp")
	}}
	n := newNotifier(q, failing)

	if err := n.processJob(context.Background(), q, deliver(metricsJob(102))); err != nil {
		t.Fatalf("unexpected error (should requeue): %v", err)
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("retried"))
	if delta := after - before; delta != 1 {
		t.Fatalf("retried counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_MaxRetriesIncrementsFailedOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("failed"))

	q := newMockQueue()
	failing := &releaseEmailMock{sendFn: func(_ context.Context, _ email.Message) error {
		return fmt.Errorf("smtp")
	}}
	n := newNotifier(q, failing)

	if err := n.processJob(context.Background(), q, redelivered(metricsJob(103), maxRetries)); err == nil {
		t.Fatal("expected error after max retries")
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("failed"))
	if delta := after - before; delta != 1 {
		t.Fatalf("failed counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_DurationObserved(t *testing.T) {
	before := histogramCount(t)

	q := newMockQueue()
	n := newNotifier(q, &releaseEmailMock{})
	_ = n.processJob(context.Background(), q, deliver(metricsJob(110)))

	after := histogramCount(t)
	if after <= before {
		t.Fatalf("expected duration histogram to have a new observation, before=%d after=%d", before, after)
	}
}
