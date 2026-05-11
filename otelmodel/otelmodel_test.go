package otelmodel

import (
	"context"
	"io"
	"testing"

	otelroot "github.com/costa92/llm-agent-otel"
	"github.com/costa92/llm-agent/llm"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func testConfig() (Config, *tracetest.InMemoryExporter) {
	exp := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exp))
	return Config{TracerProvider: tp}, exp
}

func TestWrap_PreservesCapabilities(t *testing.T) {
	cfg, _ := testConfig()
	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("full"),
		llm.WithCapabilities(llm.Capabilities{Tools: true, Embeddings: true, StructuredOutputs: true}),
		llm.WithResponses(llm.TextResponse("hello")),
	)

	wrapped := Wrap(model, cfg)
	if _, ok := wrapped.(llm.ToolCaller); !ok {
		t.Fatal("wrapped model lost ToolCaller")
	}
	if _, ok := wrapped.(llm.Embedder); !ok {
		t.Fatal("wrapped model lost Embedder")
	}
	if _, ok := wrapped.(llm.StructuredOutputs); !ok {
		t.Fatal("wrapped model lost StructuredOutputs")
	}
}

func TestGenerate_CreatesSingleSpan(t *testing.T) {
	cfg, exp := testConfig()
	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("gpt-4o-mini"),
		llm.WithResponses(llm.Response{
			Text:         "hello",
			FinishReason: llm.FinishReasonStop,
			Provider:     "scripted",
			Usage: llm.Usage{
				InputTokens:  3,
				OutputTokens: 1,
				TotalTokens:  4,
				Source:       llm.UsageReported,
			},
		}),
	)

	wrapped := Wrap(model, cfg)
	_, err := wrapped.Generate(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate(): %v", err)
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if spans[0].Name != "chat gpt-4o-mini" {
		t.Fatalf("span name = %q", spans[0].Name)
	}
}

func TestStream_CreatesSingleSpanAndFirstTokenEvent(t *testing.T) {
	cfg, exp := testConfig()
	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("gpt-4o-mini"),
		llm.WithResponses(llm.Response{
			Text:         "hello",
			FinishReason: llm.FinishReasonStop,
			Provider:     "scripted",
			Usage: llm.Usage{
				InputTokens:  3,
				OutputTokens: 1,
				TotalTokens:  4,
				Source:       llm.UsageReported,
			},
		}),
	)

	wrapped := Wrap(model, cfg)
	sr, err := wrapped.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream(): %v", err)
	}
	defer sr.Close()
	for {
		_, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next(): %v", err)
		}
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if got := len(spans[0].Events); got != 1 {
		t.Fatalf("len(events) = %d, want 1", got)
	}
	if spans[0].Events[0].Name != otelroot.EventFirstToken {
		t.Fatalf("event name = %q, want %q", spans[0].Events[0].Name, otelroot.EventFirstToken)
	}
}

func TestWithTools_RewrapsBoundModel(t *testing.T) {
	cfg, _ := testConfig()
	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("tools"),
		llm.WithCapabilities(llm.Capabilities{Tools: true}),
	)

	wrapped := Wrap(model, cfg)
	tc, ok := wrapped.(llm.ToolCaller)
	if !ok {
		t.Fatal("wrapped model missing ToolCaller")
	}
	bound, err := tc.WithTools([]llm.Tool{{Name: "calc", Parameters: []byte(`{"type":"object"}`)}})
	if err != nil {
		t.Fatalf("WithTools(): %v", err)
	}
	if _, ok := any(bound).(llm.ToolCaller); !ok {
		t.Fatal("bound wrapped model lost ToolCaller")
	}
}
