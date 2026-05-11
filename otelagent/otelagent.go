package otelagent

import (
	"context"

	agents "github.com/costa92/llm-agent"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/costa92/llm-agent-otel/otelagent"

type wrapper struct {
	inner  agents.Agent
	tp     trace.TracerProvider
	tracer trace.Tracer
}

func Wrap(agent agents.Agent, opts ...Config) agents.Agent {
	cfg := Config{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
	tp := cfg.tracerProvider()
	return &wrapper{
		inner:  agent,
		tp:     tp,
		tracer: tp.Tracer(instrumentationName),
	}
}

func (w *wrapper) Name() string { return w.inner.Name() }

func (w *wrapper) Run(ctx context.Context, input string) (agents.Result, error) {
	ctx, span := w.tracer.Start(ctx, "invoke_agent "+w.inner.Name())
	defer span.End()
	span.SetAttributes(attribute.String("agent.name", w.inner.Name()))

	ch, err := w.inner.RunStream(ctx, input)
	if err != nil {
		span.RecordError(err)
		return agents.Result{}, err
	}

	builder := runBuilder{
		tracer:  w.tracer,
		rootCtx: ctx,
		trace:   make([]agents.Step, 0, 8),
	}
	for ev := range ch {
		if ev.Done {
			if ev.Err != nil {
				builder.closeOpenSpans()
				span.RecordError(ev.Err)
				return agents.Result{}, ev.Err
			}
			builder.closeOpenSpans()
			if ev.Final != nil {
				return *ev.Final, nil
			}
			return builder.result(), nil
		}
		builder.consume(ev.Step)
	}

	builder.closeOpenSpans()
	return builder.result(), nil
}

func (w *wrapper) RunStream(ctx context.Context, input string) (<-chan agents.StepEvent, error) {
	ctx, span := w.tracer.Start(ctx, "invoke_agent "+w.inner.Name())
	span.SetAttributes(attribute.String("agent.name", w.inner.Name()))

	inner, err := w.inner.RunStream(ctx, input)
	if err != nil {
		span.RecordError(err)
		span.End()
		return nil, err
	}

	out := make(chan agents.StepEvent, 16)
	go func() {
		defer close(out)
		defer span.End()

		builder := runBuilder{
			tracer:  w.tracer,
			rootCtx: ctx,
		}
		for ev := range inner {
			if !ev.Done {
				builder.consume(ev.Step)
			} else {
				if ev.Err != nil {
					builder.closeOpenSpans()
					span.RecordError(ev.Err)
				} else {
					builder.closeOpenSpans()
				}
			}
			out <- ev
		}
	}()
	return out, nil
}

type runBuilder struct {
	tracer   trace.Tracer
	rootCtx  context.Context
	trace    []agents.Step
	answer   string
	llmCalls int
	toolSpan trace.Span
	chatSpan trace.Span
}

func (b *runBuilder) consume(step agents.Step) {
	b.trace = append(b.trace, step)
	switch step.Kind {
	case agents.StepThought, agents.StepFinal, agents.StepPlan, agents.StepReflection:
		b.closeToolSpan()
		b.startChatSpan()
		if step.Kind == agents.StepFinal {
			b.answer = step.Content
			b.closeChatSpan()
		}
	case agents.StepAction:
		b.closeChatSpan()
		b.closeToolSpan()
		_, b.toolSpan = b.tracer.Start(b.rootCtx, "execute_tool "+step.Tool)
		b.toolSpan.SetAttributes(
			attribute.String("tool.name", step.Tool),
			attribute.String("agent.step.kind", string(step.Kind)),
		)
	case agents.StepObservation:
		b.closeToolSpan()
	}
}

func (b *runBuilder) startChatSpan() {
	if b.chatSpan != nil {
		return
	}
	b.llmCalls++
	_, b.chatSpan = b.tracer.Start(b.rootCtx, "chat")
	b.chatSpan.SetAttributes(attribute.Int("agent.llm_call.index", b.llmCalls))
}

func (b *runBuilder) closeChatSpan() {
	if b.chatSpan == nil {
		return
	}
	b.chatSpan.End()
	b.chatSpan = nil
}

func (b *runBuilder) closeToolSpan() {
	if b.toolSpan == nil {
		return
	}
	b.toolSpan.End()
	b.toolSpan = nil
}

func (b *runBuilder) closeOpenSpans() {
	b.closeChatSpan()
	b.closeToolSpan()
}

func (b *runBuilder) result() agents.Result {
	return agents.Result{
		Answer: b.answer,
		Trace:  append([]agents.Step(nil), b.trace...),
		Usage:  agents.Usage{LLMCalls: b.llmCalls},
	}
}

var _ agents.Agent = (*wrapper)(nil)
