package notifier

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github-release-notifier/notifier/internal/domain"
)

func TestProcessJob_ContinuesTraceFromJob(t *testing.T) {
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(trace.ContextWithSpanContext(context.Background(), parent), carrier)

	q := newMockQueue()
	n := newNotifier(q, &releaseEmailMock{})
	job := domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "u@example.com",
		Repo:           "x/y",
		Tag:            "v1",
		UnsubToken:     "t",
		Traceparent:    carrier["traceparent"],
	}
	if err := n.processJob(context.Background(), q, deliver(job)); err != nil {
		t.Fatalf("processJob: %v", err)
	}

	var processSpan sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == "notifier.process_job" {
			processSpan = s
		}
	}
	if processSpan == nil {
		t.Fatal("notifier.process_job span was not recorded")
	}
	if processSpan.SpanContext().TraceID() != traceID {
		t.Fatalf("process_job trace = %s, want %s (should continue the producer's trace)",
			processSpan.SpanContext().TraceID(), traceID)
	}
	if processSpan.Parent().SpanID() != spanID {
		t.Fatalf("process_job parent span = %s, want %s", processSpan.Parent().SpanID(), spanID)
	}
}
