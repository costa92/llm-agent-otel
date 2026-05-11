package otelagent

import (
	"context"
	"encoding/json"
	"testing"

	agents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent/llm"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func testConfig() (Config, *tracetest.InMemoryExporter) {
	exp := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSyncer(exp))
	return Config{TracerProvider: tp}, exp
}

func TestWrap_PreservesAgentContract(t *testing.T) {
	cfg, _ := testConfig()
	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("simple-model"),
		llm.WithResponses(llm.TextResponse("hello")),
	)
	inner := agents.NewSimpleAgent(model, agents.SimpleOptions{Name: "simple"})

	wrapped := Wrap(inner, cfg)
	if wrapped.Name() != inner.Name() {
		t.Fatalf("Name() = %q, want %q", wrapped.Name(), inner.Name())
	}
	res, err := wrapped.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if res.Answer != "hello" {
		t.Fatalf("Answer = %q, want hello", res.Answer)
	}
}

func TestRun_SimpleAgent_ProducesInvokeAgentAndChatSpan(t *testing.T) {
	cfg, exp := testConfig()
	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("gpt-4o-mini"),
		llm.WithResponses(llm.Response{
			Text:         "done",
			FinishReason: llm.FinishReasonStop,
			Provider:     "scripted",
			Usage:        llm.Usage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4, Source: llm.UsageReported},
		}),
	)
	agent := agents.NewSimpleAgent(model, agents.SimpleOptions{Name: "simple"})

	res, err := Wrap(agent, cfg).Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if res.Answer != "done" {
		t.Fatalf("Answer = %q, want done", res.Answer)
	}

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("len(spans) = %d, want 2", len(spans))
	}
	assertSpanNames(t, spans, "invoke_agent simple", "chat")
	assertParentChild(t, spans, "invoke_agent simple", "chat")
}

func TestRun_ReActAgent_ProducesInvokeAgentChatExecuteToolTree(t *testing.T) {
	cfg, exp := testConfig()
	model := llm.NewScriptedLLM(
		llm.WithProvider("scripted"),
		llm.WithModel("react-model"),
		llm.WithCapabilities(llm.Capabilities{Tools: false, Embeddings: true, StructuredOutputs: true}),
		llm.WithResponses(
			llm.TextResponse("Thought: need calc\nAction: calc\nArgs: {\"x\":1}"),
			llm.TextResponse("Thought: done\nFinal: answer"),
		),
	)
	tool := agents.NewFuncTool(
		"calc",
		"test calc",
		json.RawMessage(`{"type":"object"}`),
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return "42", nil
		},
	)
	agent := agents.NewReActAgent(model, agents.ReActOptions{
		Name:     "react",
		Registry: agents.NewRegistry(tool),
		MaxSteps: 4,
	})

	res, err := Wrap(agent, cfg).Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	if res.Answer != "answer" {
		t.Fatalf("Answer = %q, want answer", res.Answer)
	}

	spans := exp.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("len(spans) = %d, want 4", len(spans))
	}
	assertSpanNames(t, spans,
		"invoke_agent react",
		"chat",
		"execute_tool calc",
		"chat",
	)
	assertParentChild(t, spans, "invoke_agent react", "chat")
	assertParentChild(t, spans, "invoke_agent react", "execute_tool calc")
	assertParentChildCount(t, spans, "invoke_agent react", "chat", 2)
}

func assertSpanNames(t *testing.T, spans tracetest.SpanStubs, want ...string) {
	t.Helper()
	if len(spans) != len(want) {
		t.Fatalf("len(spans) = %d, want %d", len(spans), len(want))
	}
	got := make([]string, 0, len(spans))
	for _, span := range spans {
		got = append(got, span.Name)
	}
	for _, name := range want {
		found := false
		for _, gotName := range got {
			if gotName == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing span %q in %v", name, got)
		}
	}
}

func assertParentChild(t *testing.T, spans tracetest.SpanStubs, parentName, childName string) {
	t.Helper()
	var parent, child *tracetest.SpanStub
	for i := range spans {
		switch spans[i].Name {
		case parentName:
			parent = &spans[i]
		case childName:
			if child == nil {
				child = &spans[i]
			}
		}
	}
	if parent == nil || child == nil {
		t.Fatalf("parent=%q child=%q not found", parentName, childName)
	}
	if child.Parent.SpanID() != parent.SpanContext.SpanID() {
		t.Fatalf("span %q parent = %s, want %s", childName, child.Parent.SpanID(), parent.SpanContext.SpanID())
	}
}

func assertParentChildCount(t *testing.T, spans tracetest.SpanStubs, parentName, childName string, want int) {
	t.Helper()
	var parent *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == parentName {
			parent = &spans[i]
			break
		}
	}
	if parent == nil {
		t.Fatalf("parent %q not found", parentName)
	}
	var got int
	for i := range spans {
		if spans[i].Name == childName && spans[i].Parent.SpanID() == parent.SpanContext.SpanID() {
			got++
		}
	}
	if got != want {
		t.Fatalf("child count for %q under %q = %d, want %d", childName, parentName, got, want)
	}
}
