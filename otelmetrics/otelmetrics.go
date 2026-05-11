package otelmetrics

import (
	"context"
	"time"

	otelroot "github.com/costa92/llm-agent-otel"
	"go.opentelemetry.io/otel/attribute"
	apimetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const instrumentationName = "github.com/costa92/llm-agent-otel/otelmetrics"

type Options struct {
	MeterProvider apimetric.MeterProvider
}

type Recorder struct {
	tokenUsage      apimetric.Int64Counter
	opDuration      apimetric.Int64Histogram
	timeToFirst     apimetric.Int64Histogram
	agentIterations apimetric.Int64Counter
	toolInvocations apimetric.Int64Counter
}

func New(opts Options) (*Recorder, error) {
	mp := opts.MeterProvider
	if mp == nil {
		mp = noop.NewMeterProvider()
	}
	meter := mp.Meter(instrumentationName)

	tokenUsage, err := meter.Int64Counter(otelroot.MetricClientTokenUsage)
	if err != nil {
		return nil, err
	}
	opDuration, err := meter.Int64Histogram(otelroot.MetricClientOperationDuration)
	if err != nil {
		return nil, err
	}
	timeToFirst, err := meter.Int64Histogram(otelroot.MetricClientOperationTTFT)
	if err != nil {
		return nil, err
	}
	agentIterations, err := meter.Int64Counter(otelroot.MetricAgentIterations)
	if err != nil {
		return nil, err
	}
	toolInvocations, err := meter.Int64Counter(otelroot.MetricAgentToolInvocations)
	if err != nil {
		return nil, err
	}
	return &Recorder{
		tokenUsage:      tokenUsage,
		opDuration:      opDuration,
		timeToFirst:     timeToFirst,
		agentIterations: agentIterations,
		toolInvocations: toolInvocations,
	}, nil
}

func (r *Recorder) RecordTokenUsage(ctx context.Context, tokens int64, opts ...apimetric.RecordOption) {
	r.tokenUsage.Add(ctx, tokens, toAddOptions(opts...)...)
}

func (r *Recorder) RecordDuration(ctx context.Context, d time.Duration, opts ...apimetric.RecordOption) {
	r.opDuration.Record(ctx, d.Milliseconds(), filterRecordOptions(opts...)...)
}

func (r *Recorder) RecordTTFT(ctx context.Context, d time.Duration, opts ...apimetric.RecordOption) {
	r.timeToFirst.Record(ctx, d.Milliseconds(), filterRecordOptions(opts...)...)
}

func (r *Recorder) RecordAgentIterations(ctx context.Context, n int64, opts ...apimetric.RecordOption) {
	r.agentIterations.Add(ctx, n, toAddOptions(opts...)...)
}

func (r *Recorder) RecordToolInvocations(ctx context.Context, n int64, opts ...apimetric.RecordOption) {
	r.toolInvocations.Add(ctx, n, toAddOptions(opts...)...)
}

func MessageAttributes(input, output string) []attribute.KeyValue {
	if !otelroot.ContentCaptureEnabled() {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String(otelroot.AttrInputMessages, otelroot.RedactText(input)),
		attribute.String(otelroot.AttrOutputMessages, otelroot.RedactText(output)),
	}
}

func filterRecordOptions(opts ...apimetric.RecordOption) []apimetric.RecordOption {
	cfg := apimetric.NewRecordConfig(opts)
	attrs := cfg.Attributes()
	filtered := filterMetricAttrs((&attrs).ToSlice())
	if len(filtered) == 0 {
		return nil
	}
	return []apimetric.RecordOption{apimetric.WithAttributeSet(attribute.NewSet(filtered...))}
}

func toAddOptions(opts ...apimetric.RecordOption) []apimetric.AddOption {
	filtered := filterRecordOptions(opts...)
	cfg := apimetric.NewRecordConfig(filtered)
	attrs := cfg.Attributes()
	if (&attrs).Len() == 0 {
		return nil
	}
	return []apimetric.AddOption{apimetric.WithAttributeSet(attrs)}
}

func filterMetricAttrs(attrs []attribute.KeyValue) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, kv := range attrs {
		switch string(kv.Key) {
		case otelroot.AttrSystem,
			otelroot.AttrRequestModel,
			otelroot.AttrOperation,
			otelroot.AttrErrorType,
			otelroot.AttrFinishReason,
			otelroot.AttrServerAddr:
			out = append(out, kv)
		}
	}
	return out
}
