package service

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github-release-notifier/internal/domain"
)

func TestScanner_CheckRepo_InjectsTraceContext(t *testing.T) {
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	otel.SetTracerProvider(sdktrace.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.TraceContext{})

	q := &mockQueue{}
	repoRepo := &scannerMockRepoRepo{
		updateTagFn: func(ctx context.Context, id int64, tag string) error { return nil },
	}
	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return []domain.Subscription{{ID: 10, Email: "a@b.com", UnsubscribeToken: "tok"}}, nil
		},
	}
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0"}, nil
		},
	}

	scanner := newScannerForCheck(repoRepo, subRepo, gh, q)
	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"}
	if err := scanner.checkRepo(context.Background(), repo); err != nil {
		t.Fatalf("checkRepo: %v", err)
	}

	if q.jobCount() != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", q.jobCount())
	}
	if q.enqueuedJobs[0].Traceparent == "" {
		t.Fatal("enqueued job must carry a W3C traceparent so the delivery service can continue the trace")
	}
}
