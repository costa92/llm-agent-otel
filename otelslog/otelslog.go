package otelslog

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type Options struct{}

type Handler struct {
	next slog.Handler
}

func NewHandler(next slog.Handler, _ Options) slog.Handler {
	if next == nil {
		next = slog.DiscardHandler
	}
	return &Handler{next: next}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, r)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(attrs)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name)}
}

var _ slog.Handler = (*Handler)(nil)
