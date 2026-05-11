package otelmodel

const (
	attrSystem          = "gen_ai.system"
	attrRequestModel    = "gen_ai.request.model"
	attrUsageInput      = "gen_ai.usage.input_tokens"
	attrUsageOutput     = "gen_ai.usage.output_tokens"
	attrUsageSource     = "gen_ai.usage.source"
	attrFinishReason    = "gen_ai.response.finish_reason"
	eventFirstToken     = "gen_ai.first_token"
	instrumentationName = "github.com/costa92/llm-agent-otel/otelmodel"
)
