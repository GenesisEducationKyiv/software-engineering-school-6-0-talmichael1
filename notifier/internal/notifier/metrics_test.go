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
	"github-release-notifier/notifier/internal/urls"
)

func histogramCount(t *testing.T) uint64 {
	t.Helper()
	var m dto.Metric
	if err := metrics.NotifierJobDuration.Write(&m); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

func TestNotifier_Metrics_SentIncrementsSentOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("sent"))

	q := newMockJobQueue()
	n := New(q, &releaseEmailMock{}, urls.Builder{BaseURL: "http://localhost:8080"}, 1)
	job := &domain.NotificationJob{SubscriptionID: 100, Email: "u@example.com", Repo: "x/y", Tag: "v1", UnsubToken: "t"}

	if err := n.processJob(context.Background(), job); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("sent"))
	if delta := after - before; delta != 1 {
		t.Fatalf("sent counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_DuplicateIncrementsDuplicateOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("duplicate"))

	q := newMockJobQueue()
	q.sent["101:v1"] = true
	n := New(q, &releaseEmailMock{}, urls.Builder{BaseURL: "http://localhost:8080"}, 1)
	job := &domain.NotificationJob{SubscriptionID: 101, Email: "u@example.com", Repo: "x/y", Tag: "v1", UnsubToken: "t"}

	if err := n.processJob(context.Background(), job); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("duplicate"))
	if delta := after - before; delta != 1 {
		t.Fatalf("duplicate counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_RetryIncrementsRetriedOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("retried"))

	q := newMockJobQueue()
	failing := &releaseEmailMock{sendFn: func(ctx context.Context, msg email.Message) error {
		return fmt.Errorf("smtp")
	}}
	n := New(q, failing, urls.Builder{BaseURL: "http://localhost:8080"}, 1)
	job := &domain.NotificationJob{SubscriptionID: 102, Email: "u@example.com", Repo: "x/y", Tag: "v1", UnsubToken: "t", Attempt: 0}

	if err := n.processJob(context.Background(), job); err != nil {
		t.Fatalf("unexpected error (should requeue): %v", err)
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("retried"))
	if delta := after - before; delta != 1 {
		t.Fatalf("retried counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_MaxRetriesIncrementsFailedOutcome(t *testing.T) {
	before := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("failed"))

	q := newMockJobQueue()
	failing := &releaseEmailMock{sendFn: func(ctx context.Context, msg email.Message) error {
		return fmt.Errorf("smtp")
	}}
	n := New(q, failing, urls.Builder{BaseURL: "http://localhost:8080"}, 1)
	job := &domain.NotificationJob{SubscriptionID: 103, Email: "u@example.com", Repo: "x/y", Tag: "v1", UnsubToken: "t", Attempt: maxRetries}

	if err := n.processJob(context.Background(), job); err == nil {
		t.Fatal("expected error after max retries")
	}

	after := testutil.ToFloat64(metrics.NotifierJobsProcessed.WithLabelValues("failed"))
	if delta := after - before; delta != 1 {
		t.Fatalf("failed counter delta = %v, want 1", delta)
	}
}

func TestNotifier_Metrics_DurationObserved(t *testing.T) {
	before := histogramCount(t)

	q := newMockJobQueue()
	n := New(q, &releaseEmailMock{}, urls.Builder{BaseURL: "http://localhost:8080"}, 1)
	job := &domain.NotificationJob{SubscriptionID: 110, Email: "u@example.com", Repo: "x/y", Tag: "v1", UnsubToken: "t"}
	_ = n.processJob(context.Background(), job)

	after := histogramCount(t)
	if after <= before {
		t.Fatalf("expected duration histogram to have a new observation, before=%d after=%d", before, after)
	}
}
