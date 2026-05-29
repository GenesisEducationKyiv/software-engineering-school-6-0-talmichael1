package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestContextHandler_AddsTraceFieldsWhenSpanPresent(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewContextHandler(inner)
	logger := slog.New(handler)

	traceID, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	spanID, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	logger.InfoContext(ctx, "hello")

	entry := decodeLog(t, buf.Bytes())
	if got := entry["trace_id"]; got != "0102030405060708090a0b0c0d0e0f10" {
		t.Fatalf("trace_id = %v, want 0102030405060708090a0b0c0d0e0f10", got)
	}
	if got := entry["span_id"]; got != "0102030405060708" {
		t.Fatalf("span_id = %v, want 0102030405060708", got)
	}
}

func TestContextHandler_OmitsTraceFieldsWhenNoSpan(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewContextHandler(inner))

	logger.InfoContext(context.Background(), "hello")

	entry := decodeLog(t, buf.Bytes())
	if _, ok := entry["trace_id"]; ok {
		t.Fatalf("trace_id should be absent, got %v", entry["trace_id"])
	}
	if _, ok := entry["span_id"]; ok {
		t.Fatalf("span_id should be absent, got %v", entry["span_id"])
	}
}

func TestContextHandler_PreservesUserAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewContextHandler(inner))

	logger.InfoContext(context.Background(), "msg", "key", "value")

	entry := decodeLog(t, buf.Bytes())
	if entry["key"] != "value" {
		t.Fatalf("key = %v, want value", entry["key"])
	}
}

func TestContextHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(NewContextHandler(inner)).With("service", "test")

	logger.InfoContext(context.Background(), "msg")

	entry := decodeLog(t, buf.Bytes())
	if entry["service"] != "test" {
		t.Fatalf("service = %v, want test", entry["service"])
	}
}

func decodeLog(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(b, &entry); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, string(b))
	}
	return entry
}
