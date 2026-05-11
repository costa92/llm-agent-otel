package main

import (
	"context"
	"log"
	"time"

	agents "github.com/costa92/llm-agent"
	otel "github.com/costa92/llm-agent-otel"
	"github.com/costa92/llm-agent-otel/otelagent"
	"github.com/costa92/llm-agent-otel/otelmodel"
	"github.com/costa92/llm-agent-otel/otelslog"
	"github.com/costa92/llm-agent/llm"
	"log/slog"
)

func main() {
	ctx := context.Background()

	tp, err := otel.NewTracerProvider(ctx, otel.DefaultExporterConfig())
	if err != nil {
		log.Fatalf("new tracer provider: %v", err)
	}
	defer func() {
		_ = tp.Shutdown(ctx)
	}()

	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("demo-model"),
		llm.WithResponses(llm.TextResponse("hello from demo")),
	)
	wrappedModel := otelmodel.Wrap(model, otelmodel.Config{TracerProvider: tp})
	agent := agents.NewSimpleAgent(wrappedModel, agents.SimpleOptions{Name: "demo"})
	wrappedAgent := otelagent.Wrap(agent, otelagent.Config{TracerProvider: tp})

	logger := slog.New(otelslog.NewHandler(slog.NewJSONHandler(log.Writer(), nil), otelslog.Options{}))
	logger.InfoContext(ctx, "demo starting", slog.String(otel.AttrSystem, "scripted"))

	if _, err := wrappedAgent.Run(ctx, "hello"); err != nil {
		log.Fatalf("run demo agent: %v", err)
	}

	logger.InfoContext(ctx, "demo completed")
	time.Sleep(2 * time.Second)
}
