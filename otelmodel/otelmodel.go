package otelmodel

import (
	"context"
	"io"

	"github.com/costa92/llm-agent/llm"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type wrapper struct {
	inner  llm.ChatModel
	tp     trace.TracerProvider
	tracer trace.Tracer
}

func Wrap(model llm.ChatModel, opts ...Config) llm.ChatModel {
	cfg := Config{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	tp := cfg.tracerProvider()
	base := &wrapper{inner: model, tp: tp, tracer: tp.Tracer(instrumentationName)}
	if tc, ok := model.(llm.ToolCaller); ok {
		if emb, ok := model.(llm.Embedder); ok {
			if so, ok := model.(llm.StructuredOutputs); ok {
				return &toolEmbedSchemaWrapper{wrapper: base, toolCaller: tc, embedder: emb, structured: so}
			}
			return &toolEmbedWrapper{wrapper: base, toolCaller: tc, embedder: emb}
		}
		if so, ok := model.(llm.StructuredOutputs); ok {
			return &toolSchemaWrapper{wrapper: base, toolCaller: tc, structured: so}
		}
		return &toolWrapper{wrapper: base, toolCaller: tc}
	}
	if emb, ok := model.(llm.Embedder); ok {
		if so, ok := model.(llm.StructuredOutputs); ok {
			return &embedSchemaWrapper{wrapper: base, embedder: emb, structured: so}
		}
		return &embedWrapper{wrapper: base, embedder: emb}
	}
	if so, ok := model.(llm.StructuredOutputs); ok {
		return &schemaWrapper{wrapper: base, structured: so}
	}
	return base
}

func (w *wrapper) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	ctx, span := w.tracer.Start(ctx, "chat "+w.inner.Info().Model)
	defer span.End()

	info := w.inner.Info()
	span.SetAttributes(
		attribute.String(attrSystem, info.Provider),
		attribute.String(attrRequestModel, info.Model),
	)

	resp, err := w.inner.Generate(ctx, req)
	if err != nil {
		span.RecordError(err)
		return resp, err
	}
	span.SetAttributes(
		attribute.Int(attrUsageInput, resp.Usage.InputTokens),
		attribute.Int(attrUsageOutput, resp.Usage.OutputTokens),
		attribute.String(attrUsageSource, string(resp.Usage.Source)),
		attribute.String(attrFinishReason, string(resp.FinishReason)),
	)
	return resp, nil
}

func (w *wrapper) Stream(ctx context.Context, req llm.Request) (llm.StreamReader, error) {
	ctx, span := w.tracer.Start(ctx, "chat "+w.inner.Info().Model)
	info := w.inner.Info()
	span.SetAttributes(
		attribute.String(attrSystem, info.Provider),
		attribute.String(attrRequestModel, info.Model),
	)
	sr, err := w.inner.Stream(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.End()
		return nil, err
	}
	return &streamReader{
		inner: sr,
		span:  span,
	}, nil
}

func (w *wrapper) Info() llm.ProviderInfo { return w.inner.Info() }

func (w *wrapper) wrap(next llm.ChatModel) llm.ChatModel {
	return Wrap(next, Config{TracerProvider: w.tp})
}

type streamReader struct {
	inner      llm.StreamReader
	span       trace.Span
	sawContent bool
	closed     bool
}

func (r *streamReader) Next() (llm.StreamEvent, error) {
	ev, err := r.inner.Next()
	if err != nil {
		if err != io.EOF {
			r.span.RecordError(err)
		}
		r.end()
		return ev, err
	}
	if !r.sawContent && ev.Kind != llm.EventDone {
		r.sawContent = true
		r.span.AddEvent(eventFirstToken)
	}
	if ev.Kind == llm.EventDone && ev.Usage != nil {
		r.span.SetAttributes(
			attribute.Int(attrUsageInput, ev.Usage.InputTokens),
			attribute.Int(attrUsageOutput, ev.Usage.OutputTokens),
			attribute.String(attrUsageSource, string(ev.Usage.Source)),
			attribute.String(attrFinishReason, string(ev.FinishReason)),
		)
		r.end()
	}
	return ev, nil
}

func (r *streamReader) Close() error {
	err := r.inner.Close()
	r.end()
	return err
}

func (r *streamReader) end() {
	if r.closed {
		return
	}
	r.closed = true
	r.span.End()
}

type toolWrapper struct {
	*wrapper
	toolCaller llm.ToolCaller
}

func (w *toolWrapper) WithTools(tools []llm.Tool) (llm.ToolCaller, error) {
	next, err := w.toolCaller.WithTools(tools)
	if err != nil {
		return nil, err
	}
	wrapped := w.wrap(next)
	tc, _ := wrapped.(llm.ToolCaller)
	return tc, nil
}

type embedWrapper struct {
	*wrapper
	embedder llm.Embedder
}

func (w *embedWrapper) Embed(ctx context.Context, texts []string) ([]llm.Vector, llm.Usage, error) {
	ctx, span := w.tracer.Start(ctx, "embed "+w.inner.Info().Model)
	defer span.End()
	info := w.inner.Info()
	span.SetAttributes(
		attribute.String(attrSystem, info.Provider),
		attribute.String(attrRequestModel, info.Model),
	)
	vectors, usage, err := w.embedder.Embed(ctx, texts)
	if err != nil {
		span.RecordError(err)
		return nil, llm.Usage{}, err
	}
	span.SetAttributes(
		attribute.Int(attrUsageInput, usage.InputTokens),
		attribute.Int(attrUsageOutput, usage.OutputTokens),
		attribute.String(attrUsageSource, string(usage.Source)),
	)
	return vectors, usage, nil
}

func (w *embedWrapper) EmbedDimensions() int { return w.embedder.EmbedDimensions() }

type schemaWrapper struct {
	*wrapper
	structured llm.StructuredOutputs
}

func (w *schemaWrapper) WithSchema(schema []byte) (llm.ChatModel, error) {
	next, err := w.structured.WithSchema(schema)
	if err != nil {
		return nil, err
	}
	return w.wrap(next), nil
}

type toolEmbedWrapper struct {
	*wrapper
	toolCaller llm.ToolCaller
	embedder   llm.Embedder
}

func (w *toolEmbedWrapper) WithTools(tools []llm.Tool) (llm.ToolCaller, error) {
	next, err := w.toolCaller.WithTools(tools)
	if err != nil {
		return nil, err
	}
	tc, _ := w.wrap(next).(llm.ToolCaller)
	return tc, nil
}

func (w *toolEmbedWrapper) Embed(ctx context.Context, texts []string) ([]llm.Vector, llm.Usage, error) {
	return (&embedWrapper{wrapper: w.wrapper, embedder: w.embedder}).Embed(ctx, texts)
}

func (w *toolEmbedWrapper) EmbedDimensions() int { return w.embedder.EmbedDimensions() }

type toolSchemaWrapper struct {
	*wrapper
	toolCaller llm.ToolCaller
	structured llm.StructuredOutputs
}

func (w *toolSchemaWrapper) WithTools(tools []llm.Tool) (llm.ToolCaller, error) {
	next, err := w.toolCaller.WithTools(tools)
	if err != nil {
		return nil, err
	}
	tc, _ := w.wrap(next).(llm.ToolCaller)
	return tc, nil
}

func (w *toolSchemaWrapper) WithSchema(schema []byte) (llm.ChatModel, error) {
	next, err := w.structured.WithSchema(schema)
	if err != nil {
		return nil, err
	}
	return w.wrap(next), nil
}

type embedSchemaWrapper struct {
	*wrapper
	embedder   llm.Embedder
	structured llm.StructuredOutputs
}

func (w *embedSchemaWrapper) Embed(ctx context.Context, texts []string) ([]llm.Vector, llm.Usage, error) {
	return (&embedWrapper{wrapper: w.wrapper, embedder: w.embedder}).Embed(ctx, texts)
}

func (w *embedSchemaWrapper) EmbedDimensions() int { return w.embedder.EmbedDimensions() }

func (w *embedSchemaWrapper) WithSchema(schema []byte) (llm.ChatModel, error) {
	next, err := w.structured.WithSchema(schema)
	if err != nil {
		return nil, err
	}
	return w.wrap(next), nil
}

type toolEmbedSchemaWrapper struct {
	*wrapper
	toolCaller llm.ToolCaller
	embedder   llm.Embedder
	structured llm.StructuredOutputs
}

func (w *toolEmbedSchemaWrapper) WithTools(tools []llm.Tool) (llm.ToolCaller, error) {
	next, err := w.toolCaller.WithTools(tools)
	if err != nil {
		return nil, err
	}
	tc, _ := w.wrap(next).(llm.ToolCaller)
	return tc, nil
}

func (w *toolEmbedSchemaWrapper) Embed(ctx context.Context, texts []string) ([]llm.Vector, llm.Usage, error) {
	return (&embedWrapper{wrapper: w.wrapper, embedder: w.embedder}).Embed(ctx, texts)
}

func (w *toolEmbedSchemaWrapper) EmbedDimensions() int { return w.embedder.EmbedDimensions() }

func (w *toolEmbedSchemaWrapper) WithSchema(schema []byte) (llm.ChatModel, error) {
	next, err := w.structured.WithSchema(schema)
	if err != nil {
		return nil, err
	}
	return w.wrap(next), nil
}

var (
	_ llm.ChatModel         = (*wrapper)(nil)
	_ llm.ChatModel         = (*toolWrapper)(nil)
	_ llm.ToolCaller        = (*toolWrapper)(nil)
	_ llm.ChatModel         = (*embedWrapper)(nil)
	_ llm.Embedder          = (*embedWrapper)(nil)
	_ llm.ChatModel         = (*schemaWrapper)(nil)
	_ llm.StructuredOutputs = (*schemaWrapper)(nil)
	_ llm.ChatModel         = (*toolEmbedWrapper)(nil)
	_ llm.ToolCaller        = (*toolEmbedWrapper)(nil)
	_ llm.Embedder          = (*toolEmbedWrapper)(nil)
	_ llm.ChatModel         = (*toolSchemaWrapper)(nil)
	_ llm.ToolCaller        = (*toolSchemaWrapper)(nil)
	_ llm.StructuredOutputs = (*toolSchemaWrapper)(nil)
	_ llm.ChatModel         = (*embedSchemaWrapper)(nil)
	_ llm.Embedder          = (*embedSchemaWrapper)(nil)
	_ llm.StructuredOutputs = (*embedSchemaWrapper)(nil)
	_ llm.ChatModel         = (*toolEmbedSchemaWrapper)(nil)
	_ llm.ToolCaller        = (*toolEmbedSchemaWrapper)(nil)
	_ llm.Embedder          = (*toolEmbedSchemaWrapper)(nil)
	_ llm.StructuredOutputs = (*toolEmbedSchemaWrapper)(nil)
)
