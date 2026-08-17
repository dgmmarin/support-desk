package clog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// test_correlation_id_propagates_through_context (ISSUE-0001, NFR-R-01)
func TestCorrelationIDPropagatesThroughContext(t *testing.T) {
	ctx := WithCorrelationID(context.Background(), "abc123")
	if got := CorrelationID(ctx); got != "abc123" {
		t.Fatalf("CorrelationID = %q, want %q", got, "abc123")
	}

	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	Logger(ctx, base).Info("hello")

	out := buf.String()
	if !strings.Contains(out, FieldName) {
		t.Fatalf("log record missing %q field: %s", FieldName, out)
	}
	if !strings.Contains(out, "abc123") {
		t.Fatalf("log record missing correlation id value: %s", out)
	}
}

func TestLoggerWithoutIDIsUnchanged(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	Logger(context.Background(), base).Info("hello")
	if strings.Contains(buf.String(), FieldName) {
		t.Fatalf("did not expect a correlation id field: %s", buf.String())
	}
}

func TestEnsureGeneratesUniqueIDs(t *testing.T) {
	a := CorrelationID(Ensure(context.Background()))
	b := CorrelationID(Ensure(context.Background()))
	if a == "" || b == "" {
		t.Fatal("Ensure must produce a non-empty id")
	}
	if a == b {
		t.Fatal("Ensure should mint distinct ids")
	}
}
